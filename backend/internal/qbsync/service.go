package qbsync

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"finopshq/internal/apperr"
	"finopshq/internal/audit"
	"finopshq/internal/authz"
	"finopshq/internal/input"
	"finopshq/internal/money"
	"finopshq/internal/quickbooks"
	"finopshq/internal/secrets"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

type Service struct {
	Pool          *pgxpool.Pool
	Mode          string // "simulated" or "live"
	Environment   string
	OAuth         *quickbooks.OAuth
	Box           *secrets.Box
	WebhookSecret string
	// Kick is called after a job is queued so an in-process worker can start without waiting.
	Kick func()
}

type Connection struct {
	ID             uuid.UUID  `json:"id"`
	Provider       string     `json:"provider"`
	Mode           string     `json:"mode"`
	Status         string     `json:"status"`
	RealmID        *string    `json:"realm_id"`
	CompanyName    *string    `json:"company_name"`
	ConnectedAt    *time.Time `json:"connected_at"`
	LastSyncedAt   *time.Time `json:"last_synced_at"`
	LastError      *string    `json:"last_error"`
	TokenExpiresAt *time.Time `json:"refresh_token_expires_at"`
}

func connectionOf(c store.SyncConnection) *Connection {
	return &Connection{ID: c.ID, Provider: c.Provider, Mode: c.Mode, Status: c.Status, RealmID: c.RealmID, CompanyName: c.CompanyName,
		ConnectedAt: c.ConnectedAt, LastSyncedAt: c.LastSyncedAt, LastError: c.LastError, TokenExpiresAt: c.RefreshTokenExpiresAt}
}

type Job struct {
	ID              uuid.UUID       `json:"id"`
	Kind            string          `json:"kind"`
	Trigger         string          `json:"trigger"`
	Status          string          `json:"status"`
	Mode            string          `json:"mode,omitempty"`
	Attempts        int32           `json:"attempts"`
	MaxAttempts     int32           `json:"max_attempts"`
	RunAt           time.Time       `json:"run_at"`
	StartedAt       *time.Time      `json:"started_at"`
	FinishedAt      *time.Time      `json:"finished_at"`
	LastError       *string         `json:"last_error"`
	Stats           json.RawMessage `json:"stats"`
	RetryOf         *uuid.UUID      `json:"retry_of"`
	RequestedByName *string         `json:"requested_by_name,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

func jobOf(j store.SyncJob) Job {
	return Job{ID: j.ID, Kind: j.Kind, Trigger: j.Trigger, Status: j.Status, Attempts: j.Attempts, MaxAttempts: j.MaxAttempts, RunAt: j.RunAt,
		StartedAt: j.StartedAt, FinishedAt: j.FinishedAt, LastError: j.LastError, Stats: j.Stats, RetryOf: j.RetryOf, CreatedAt: j.CreatedAt}
}

func (s *Service) tx(ctx context.Context, p authz.Principal, perm authz.Permission, fn func(q *store.Queries) error) error {
	if err := p.Require(perm); err != nil {
		return err
	}
	return tenant.WithTx(ctx, s.Pool, p.Scope(), func(tx pgx.Tx) error { return fn(store.New(tx)) })
}

type Status struct {
	Mode       string      `json:"available_mode"`
	Simulated  bool        `json:"simulated"`
	Connection *Connection `json:"connection"`
	Jobs       []Job       `json:"recent_jobs"`
}

func (s *Service) Status(ctx context.Context, p authz.Principal) (Status, error) {
	st := Status{Mode: s.Mode, Simulated: s.Mode == quickbooks.ModeSimulated, Jobs: []Job{}}
	err := s.tx(ctx, p, authz.IntegrationsView, func(q *store.Queries) error {
		c, err := q.GetConnection(ctx, p.Org.ID)
		if err == nil {
			st.Connection = connectionOf(c)
		} else if !apperr.IsNoRows(err) {
			return err
		}
		rows, err := q.ListSyncJobs(ctx, store.ListSyncJobsParams{RowLimit: 10})
		if err != nil {
			return err
		}
		for _, r := range rows {
			st.Jobs = append(st.Jobs, listJob(r))
		}
		return nil
	})
	return st, err
}

func listJob(r store.ListSyncJobsRow) Job {
	j := jobOf(store.SyncJob{ID: r.ID, Kind: r.Kind, Trigger: r.Trigger, Status: r.Status, Attempts: r.Attempts, MaxAttempts: r.MaxAttempts,
		RunAt: r.RunAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, LastError: r.LastError, Stats: r.Stats, RetryOf: r.RetryOf, CreatedAt: r.CreatedAt})
	j.Mode, j.RequestedByName = r.ConnectionMode, r.RequestedByName
	return j
}

type ConnectResult struct {
	AuthorizeURL string      `json:"authorize_url,omitempty"`
	Connection   *Connection `json:"connection,omitempty"`
}

// Connect starts the OAuth flow (live) or creates a labelled simulated connection.
func (s *Service) Connect(ctx context.Context, p authz.Principal) (ConnectResult, error) {
	var res ConnectResult
	err := s.tx(ctx, p, authz.IntegrationsManage, func(q *store.Queries) error {
		if s.Mode == quickbooks.ModeSimulated {
			name := "Simulated QuickBooks company (sample data)"
			c, err := q.UpsertConnection(ctx, store.UpsertConnectionParams{OrganizationID: p.Org.ID, Mode: quickbooks.ModeSimulated,
				CompanyName: &name, ConnectedBy: &p.User.ID})
			if err != nil {
				return err
			}
			res.Connection = connectionOf(c)
			return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "integration.connected", EntityType: "sync_connection", EntityID: c.ID,
				After: map[string]any{"provider": "quickbooks", "mode": "simulated"}})
		}
		if s.OAuth == nil {
			return apperr.Unprocessable("not_configured", "QuickBooks credentials are not configured on the server")
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return err
		}
		secret := base64.RawURLEncoding.EncodeToString(raw)
		sum := sha256.Sum256([]byte(secret))
		if err := q.CreateOAuthState(ctx, store.CreateOAuthStateParams{OrganizationID: p.Org.ID, UserID: p.User.ID, StateHash: sum[:],
			ExpiresAt: time.Now().Add(10 * time.Minute)}); err != nil {
			return err
		}
		res.AuthorizeURL = s.OAuth.AuthCodeURL(p.Org.ID.String() + "." + secret)
		return nil
	})
	return res, err
}

// Callback completes the OAuth flow. The state names the organization and must be unused,
// unexpired and issued to the same signed-in user.
func (s *Service) Callback(ctx context.Context, userID uuid.UUID, state, code, realmID string) (uuid.UUID, error) {
	orgPart, secret, ok := strings.Cut(state, ".")
	orgID, err := uuid.Parse(orgPart)
	if !ok || err != nil || secret == "" || code == "" || realmID == "" {
		return uuid.Nil, apperr.BadRequest("invalid OAuth callback")
	}
	if s.OAuth == nil || s.Box == nil {
		return orgID, apperr.Unprocessable("not_configured", "QuickBooks credentials are not configured on the server")
	}
	scope := tenant.Scope{OrgID: orgID, UserID: userID}
	err = tenant.WithTx(ctx, s.Pool, scope, func(tx pgx.Tx) error {
		q := store.New(tx)
		m, err := q.GetMembershipForUser(ctx, store.GetMembershipForUserParams{OrganizationID: orgID, UserID: userID})
		if err != nil || !authz.Role(m.Role).Can(authz.IntegrationsManage) {
			return apperr.Forbidden("you cannot connect integrations for this organization")
		}
		sum := sha256.Sum256([]byte(secret))
		st, err := q.ConsumeOAuthState(ctx, sum[:])
		if apperr.IsNoRows(err) || (err == nil && st.UserID != userID) {
			return apperr.BadRequest("the authorization request has expired or was already used; start again")
		}
		if err != nil {
			return err
		}
		tok, err := s.OAuth.Exchange(ctx, code)
		if err != nil {
			return apperr.New(http.StatusBadGateway, "oauth_exchange_failed", "QuickBooks did not accept the authorization: "+err.Error())
		}
		accessEnc, err := s.Box.Seal([]byte(tok.AccessToken), orgID[:])
		if err != nil {
			return err
		}
		refreshEnc, err := s.Box.Seal([]byte(tok.RefreshToken), orgID[:])
		if err != nil {
			return err
		}
		c, err := q.UpsertConnection(ctx, store.UpsertConnectionParams{OrganizationID: orgID, Mode: quickbooks.ModeLive, RealmID: &realmID,
			AccessTokenEnc: accessEnc, RefreshTokenEnc: refreshEnc, AccessTokenExpiresAt: &tok.AccessExpiry, RefreshTokenExpiresAt: &tok.RefreshExpiry,
			ConnectedBy: &userID})
		if apperr.IsUniqueViolation(err, "sync_connections_realm_key") {
			return apperr.Conflict("realm_in_use", "this QuickBooks company is already connected to another organization")
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, scope, audit.Entry{Action: "integration.connected", EntityType: "sync_connection", EntityID: c.ID,
			After: map[string]any{"provider": "quickbooks", "mode": "live", "realm_id": realmID, "environment": s.Environment}})
	})
	return orgID, err
}

func (s *Service) Disconnect(ctx context.Context, p authz.Principal) error {
	return s.tx(ctx, p, authz.IntegrationsManage, func(q *store.Queries) error {
		c, err := q.GetConnection(ctx, p.Org.ID)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("connection")
		}
		if err != nil {
			return err
		}
		if c.Mode == quickbooks.ModeLive && s.OAuth != nil && s.Box != nil && len(c.RefreshTokenEnc) > 0 {
			if rt, err := s.Box.Open(c.RefreshTokenEnc, p.Org.ID[:]); err == nil {
				if err := s.OAuth.Revoke(ctx, string(rt)); err != nil {
					slog.Warn("token revocation failed; tokens are deleted locally anyway", "err", err)
				}
			}
		}
		if _, err := q.DisconnectConnection(ctx, c.ID); err != nil {
			return err
		}
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "integration.disconnected", EntityType: "sync_connection", EntityID: c.ID,
			Before: map[string]any{"status": c.Status, "mode": c.Mode, "realm_id": c.RealmID}, After: map[string]any{"status": "disconnected"}})
	})
}

var kinds = []string{"full", "customers", "invoices", "payments"}

type EnqueueResult struct {
	Job      Job  `json:"job"`
	Existing bool `json:"existing"`
}

func enqueue(ctx context.Context, q *store.Queries, orgID uuid.UUID, requestedBy *uuid.UUID, kind, trigger, key string, retryOf *uuid.UUID) (store.SyncJob, bool, error) {
	if job, err := q.GetSyncJobByKey(ctx, store.GetSyncJobByKeyParams{OrganizationID: orgID, IdempotencyKey: key}); err == nil {
		return job, true, nil
	} else if !apperr.IsNoRows(err) {
		return store.SyncJob{}, false, err
	}
	c, err := q.GetConnection(ctx, orgID)
	if apperr.IsNoRows(err) {
		return store.SyncJob{}, false, apperr.Unprocessable("not_connected", "connect QuickBooks before starting a sync")
	}
	if err != nil {
		return store.SyncJob{}, false, err
	}
	if c.Status != "connected" {
		return store.SyncJob{}, false, apperr.Unprocessable("not_connected", "the QuickBooks connection is "+c.Status)
	}
	if active, err := q.GetActiveSyncJob(ctx, store.GetActiveSyncJobParams{ConnectionID: c.ID, Kind: kind}); err == nil {
		return active, true, nil
	} else if !apperr.IsNoRows(err) {
		return store.SyncJob{}, false, err
	}
	job, err := q.EnqueueSyncJob(ctx, store.EnqueueSyncJobParams{OrganizationID: orgID, ConnectionID: c.ID, Kind: kind, Trigger: trigger,
		IdempotencyKey: key, RequestedBy: requestedBy, MaxAttempts: 5, RetryOf: retryOf})
	if apperr.IsUniqueViolation(err, "") {
		// A concurrent request created the same job; return it instead.
		return store.SyncJob{}, false, errRace
	}
	return job, false, err
}

var errRace = errors.New("concurrent enqueue")

func (s *Service) Enqueue(ctx context.Context, p authz.Principal, kind, key string) (EnqueueResult, error) {
	if !contains(kinds, kind) {
		return EnqueueResult{}, (&apperr.Validation{}).With("kind", "must be full, customers, invoices or payments")
	}
	if key == "" {
		key = "manual-" + uuid.NewString()
	}
	if len(key) > 100 {
		return EnqueueResult{}, apperr.BadRequest("Idempotency-Key is too long")
	}
	var res EnqueueResult
	for attempt := 0; attempt < 2; attempt++ {
		err := s.tx(ctx, p, authz.SyncRun, func(q *store.Queries) error {
			job, existing, err := enqueue(ctx, q, p.Org.ID, &p.User.ID, kind, "manual", key, nil)
			if err != nil {
				return err
			}
			res = EnqueueResult{Job: jobOf(job), Existing: existing}
			if existing {
				return nil
			}
			return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "sync.requested", EntityType: "sync_job", EntityID: job.ID,
				After: map[string]any{"kind": kind, "trigger": "manual"}})
		})
		if errors.Is(err, errRace) {
			continue
		}
		if err == nil && !res.Existing && s.Kick != nil {
			s.Kick()
		}
		return res, err
	}
	return res, apperr.Conflict("busy", "another sync request is being created; try again")
}

func (s *Service) Retry(ctx context.Context, p authz.Principal, jobID uuid.UUID) (EnqueueResult, error) {
	var res EnqueueResult
	err := s.tx(ctx, p, authz.SyncRun, func(q *store.Queries) error {
		old, err := q.GetSyncJob(ctx, jobID)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("sync job")
		}
		if err != nil {
			return err
		}
		if old.Status != "failed" {
			return apperr.Unprocessable("not_failed", "only failed jobs can be retried")
		}
		job, existing, err := enqueue(ctx, q, p.Org.ID, &p.User.ID, old.Kind, "retry", "retry-"+uuid.NewString(), &old.ID)
		if err != nil {
			return err
		}
		res = EnqueueResult{Job: jobOf(job), Existing: existing}
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "sync.retried", EntityType: "sync_job", EntityID: job.ID,
			Extra: map[string]any{"retry_of": old.ID, "kind": old.Kind}})
	})
	if err == nil && s.Kick != nil {
		s.Kick()
	}
	return res, err
}

type JobList struct {
	Data  []Job `json:"data"`
	Total int64 `json:"total"`
}

func (s *Service) ListJobs(ctx context.Context, p authz.Principal, status *string, page input.Page) (JobList, error) {
	res := JobList{Data: []Job{}}
	err := s.tx(ctx, p, authz.IntegrationsView, func(q *store.Queries) error {
		rows, err := q.ListSyncJobs(ctx, store.ListSyncJobsParams{Status: status, RowLimit: page.Limit, RowOffset: page.Offset})
		if err != nil {
			return err
		}
		for _, r := range rows {
			res.Data = append(res.Data, listJob(r))
		}
		res.Total, err = q.CountSyncJobs(ctx, status)
		return err
	})
	return res, err
}

type Attempt struct {
	Attempt    int32           `json:"attempt"`
	Worker     string          `json:"worker"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	Outcome    string          `json:"outcome"`
	Error      *string         `json:"error"`
	Stats      json.RawMessage `json:"stats"`
}

func (s *Service) GetJob(ctx context.Context, p authz.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, authz.IntegrationsView, func(q *store.Queries) error {
		j, err := q.GetSyncJob(ctx, id)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("sync job")
		}
		if err != nil {
			return err
		}
		rows, err := q.ListSyncJobAttempts(ctx, id)
		if err != nil {
			return err
		}
		attempts := make([]Attempt, 0, len(rows))
		for _, a := range rows {
			attempts = append(attempts, Attempt{Attempt: a.Attempt, Worker: a.Worker, StartedAt: a.StartedAt, FinishedAt: a.FinishedAt,
				Outcome: a.Outcome, Error: a.Error, Stats: a.Stats})
		}
		out = map[string]any{"job": jobOf(j), "attempts": attempts}
		return nil
	})
	return out, err
}

type ExternalRecord struct {
	ID                uuid.UUID  `json:"id"`
	EntityType        string     `json:"entity_type"`
	ExternalID        string     `json:"external_id"`
	DocNumber         *string    `json:"doc_number"`
	DisplayName       *string    `json:"display_name"`
	InvoiceExternalID *string    `json:"invoice_external_id"`
	TxnDate           *string    `json:"txn_date"`
	DueDate           *string    `json:"due_date"`
	Amount            *string    `json:"amount"`
	Balance           *string    `json:"balance"`
	Currency          *string    `json:"currency"`
	Simulated         bool       `json:"simulated"`
	ExternalUpdatedAt *time.Time `json:"external_updated_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func ExternalOf(r store.ExternalRecord) ExternalRecord {
	cur := ""
	if r.Currency != nil {
		cur = *r.Currency
	}
	fmtAmt := func(d *decimal.Decimal) *string {
		if d == nil {
			return nil
		}
		v := money.Format(*d, cur)
		return &v
	}
	return ExternalRecord{ID: r.ID, EntityType: r.EntityType, ExternalID: r.ExternalID, DocNumber: r.DocNumber, DisplayName: r.DisplayName,
		InvoiceExternalID: r.InvoiceExternalID, TxnDate: input.FormatDatePtr(r.TxnDate), DueDate: input.FormatDatePtr(r.DueDate),
		Amount: fmtAmt(r.Amount), Balance: fmtAmt(r.Balance), Currency: r.Currency,
		Simulated: strings.Contains(string(r.Raw), `"simulated":true`), ExternalUpdatedAt: r.ExternalUpdatedAt, UpdatedAt: r.UpdatedAt}
}

func (s *Service) ListExternal(ctx context.Context, p authz.Principal, entity *string, page input.Page) ([]ExternalRecord, error) {
	out := []ExternalRecord{}
	err := s.tx(ctx, p, authz.IntegrationsView, func(q *store.Queries) error {
		rows, err := q.ListExternalRecords(ctx, store.ListExternalRecordsParams{EntityType: entity, RowLimit: page.Limit, RowOffset: page.Offset})
		for _, r := range rows {
			out = append(out, ExternalOf(r))
		}
		return err
	})
	return out, err
}

// Webhook verifies an Intuit notification and queues a sync for each affected company.
func (s *Service) Webhook(ctx context.Context, body []byte, signature string) (int, error) {
	if !quickbooks.VerifyWebhook(body, signature, s.WebhookSecret) {
		return 0, apperr.New(http.StatusUnauthorized, "bad_signature", "webhook signature is invalid")
	}
	var payload quickbooks.WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, apperr.BadRequest("invalid webhook payload")
	}
	digest := sha256.Sum256(body)
	queued := 0
	for _, n := range payload.EventNotifications {
		var connID, orgID uuid.UUID
		err := s.Pool.QueryRow(ctx, "SELECT connection_id, organization_id FROM find_quickbooks_connection($1)", n.RealmID).Scan(&connID, &orgID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return queued, err
		}
		key := fmt.Sprintf("webhook-%s-%s", hex.EncodeToString(digest[:8]), n.RealmID)
		err = tenant.WithTx(ctx, s.Pool, tenant.Scope{OrgID: orgID}, func(tx pgx.Tx) error {
			_, existing, err := enqueue(ctx, store.New(tx), orgID, nil, "full", "webhook", key, nil)
			if err == nil && !existing {
				queued++
			}
			if errors.Is(err, errRace) {
				return nil
			}
			return err
		})
		if err != nil {
			var ae *apperr.Error
			if !errors.As(err, &ae) {
				return queued, err
			}
		}
	}
	if queued > 0 && s.Kick != nil {
		s.Kick()
	}
	return queued, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// RecentJobs feeds the dashboard's sync activity panel.
func RecentJobs(ctx context.Context, q *store.Queries, limit int32) (any, error) {
	rows, err := q.ListSyncJobs(ctx, store.ListSyncJobsParams{RowLimit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(rows))
	for _, r := range rows {
		out = append(out, listJob(r))
	}
	return out, nil
}
