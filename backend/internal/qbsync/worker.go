package qbsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	gosync "sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"finopshq/internal/audit"
	"finopshq/internal/quickbooks"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

// AfterSync runs once a job has imported invoices or payments; reconciliation plugs in here.
type AfterSync func(ctx context.Context, orgID, connectionID uuid.UUID) (any, error)

type Worker struct {
	Pool        *pgxpool.Pool
	ID          string
	Live        quickbooks.Provider
	Simulated   quickbooks.Provider
	Tokens      *TokenManager
	PageSize    int
	Lease       time.Duration
	Poll        time.Duration
	Concurrency int
	Backoff     func(attempt int) time.Duration
	AfterSync   AfterSync

	wakeOnce gosync.Once
	wake     chan struct{}
}

func (w *Worker) wakeCh() chan struct{} {
	w.wakeOnce.Do(func() { w.wake = make(chan struct{}, 1) })
	return w.wake
}

// Notify wakes an idle worker loop immediately (used after a job is queued in-process).
func (w *Worker) Notify() {
	select {
	case w.wakeCh() <- struct{}{}:
	default:
	}
}

func (w *Worker) defaults() {
	if w.ID == "" {
		host, _ := os.Hostname()
		w.ID = fmt.Sprintf("%s-%d-%s", host, os.Getpid(), uuid.NewString()[:8])
	}
	if w.PageSize <= 0 {
		w.PageSize = 100
	}
	if w.Lease <= 0 {
		w.Lease = 2 * time.Minute
	}
	if w.Poll <= 0 {
		w.Poll = 2 * time.Second
	}
	if w.Concurrency <= 0 {
		w.Concurrency = 2
	}
	if w.Backoff == nil {
		w.Backoff = ExponentialBackoff
	}
}

// ExponentialBackoff: 30s, 60s, 120s ... capped at 30 minutes, with ±20% jitter.
func ExponentialBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := 30 * time.Second << (attempt - 1)
	if d > 30*time.Minute || d <= 0 {
		d = 30 * time.Minute
	}
	jitter := time.Duration(rand.Int64N(int64(d)/5*2+1)) - d/5
	return d + jitter
}

// Run processes jobs until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.defaults()
	slog.Info("sync worker started", "worker", w.ID, "concurrency", w.Concurrency)
	var wg gosync.WaitGroup
	for i := 0; i < w.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				worked, err := w.RunOnce(ctx)
				if err != nil && ctx.Err() == nil {
					slog.Error("sync worker iteration failed", "err", err)
				}
				if !worked {
					select {
					case <-ctx.Done():
					case <-w.wakeCh():
					case <-time.After(w.Poll):
					}
				}
			}
		}()
	}
	wg.Wait()
	slog.Info("sync worker stopped", "worker", w.ID)
}

type claimed struct {
	JobID       uuid.UUID
	OrgID       uuid.UUID
	Attempt     int32
	MaxAttempts int32
}

// RunOnce claims and processes at most one job. It reports whether a job was found.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	w.defaults()
	var c claimed
	err := w.Pool.QueryRow(ctx, "SELECT job_id, organization_id, attempt, max_attempts FROM claim_sync_job($1, $2)", w.ID, int(w.Lease.Seconds())).
		Scan(&c.JobID, &c.OrgID, &c.Attempt, &c.MaxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim job: %w", err)
	}
	w.process(ctx, c)
	return true, nil
}

type permanentError struct{ error }

func (w *Worker) process(ctx context.Context, c claimed) {
	scope := tenant.Scope{OrgID: c.OrgID}
	started := time.Now()
	stats := &Stats{}
	log := slog.With("job_id", c.JobID, "org_id", c.OrgID, "attempt", c.Attempt, "worker", w.ID)

	var job store.SyncJob
	var conn store.SyncConnection
	var baseCurrency string
	err := tenant.WithTx(ctx, w.Pool, scope, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		if job, err = q.GetSyncJob(ctx, c.JobID); err != nil {
			return err
		}
		if conn, err = q.GetConnectionByID(ctx, job.ConnectionID); err != nil {
			return err
		}
		org, err := q.GetOrganization(ctx, c.OrgID)
		baseCurrency = org.BaseCurrency
		return err
	})
	if err == nil && c.Attempt > c.MaxAttempts {
		err = permanentError{fmt.Errorf("gave up after %d attempts (worker lease expired repeatedly)", c.MaxAttempts)}
	}
	if err == nil && conn.Status != "connected" {
		err = permanentError{fmt.Errorf("the QuickBooks connection is %s; reconnect it and retry", conn.Status)}
	}
	if err == nil {
		err = w.run(ctx, job, conn, baseCurrency, stats)
	}
	if err == nil && w.AfterSync != nil && job.Kind != "customers" {
		var rec any
		if rec, err = w.AfterSync(ctx, c.OrgID, conn.ID); err == nil {
			stats.Reconcile = rec
		}
	}
	w.finish(ctx, c, job, conn, stats, started, err, log)
}

func (w *Worker) provider(conn store.SyncConnection) quickbooks.Provider {
	if conn.Mode == quickbooks.ModeSimulated {
		return w.Simulated
	}
	return w.Live
}

func (w *Worker) run(ctx context.Context, job store.SyncJob, conn store.SyncConnection, base string, stats *Stats) error {
	prov := w.provider(conn)
	if prov == nil {
		return permanentError{fmt.Errorf("no %s QuickBooks provider is configured", conn.Mode)}
	}
	sess := quickbooks.Session{OrgID: job.OrganizationID, BaseCurrency: base}
	if conn.RealmID != nil {
		sess.RealmID = *conn.RealmID
	}
	refreshed := false
	token := func(force bool) error {
		if conn.Mode != quickbooks.ModeLive {
			return nil
		}
		if w.Tokens == nil {
			return permanentError{errors.New("live QuickBooks is not configured on this worker")}
		}
		t, err := w.Tokens.AccessToken(ctx, job.OrganizationID, conn.ID, force)
		if errors.Is(err, quickbooks.ErrUnauthorized) {
			return permanentError{err}
		}
		sess.AccessToken = t
		return err
	}
	if err := token(false); err != nil {
		return err
	}
	im := &importer{pool: w.Pool, scope: tenant.Scope{OrgID: job.OrganizationID}, conn: conn, jobID: job.ID, stats: stats}

	// pageLoop fetches pages until a short page, refreshing the token once on a 401.
	pageLoop := func(fetch func(start int) (int, error)) error {
		for start := 1; ; start += w.PageSize {
			n, err := fetch(start)
			if errors.Is(err, quickbooks.ErrUnauthorized) && !refreshed && conn.Mode == quickbooks.ModeLive {
				refreshed = true
				if err := token(true); err != nil {
					return err
				}
				n, err = fetch(start)
			}
			if errors.Is(err, quickbooks.ErrUnauthorized) {
				markNeedsReauth(ctx, w.Pool, job.OrganizationID, conn.ID, "QuickBooks rejected the access token; reconnect the integration")
				return permanentError{err}
			}
			if err != nil {
				return err
			}
			stats.Pages++
			if err := w.heartbeat(ctx, job); err != nil {
				return err
			}
			if n < w.PageSize {
				return nil
			}
		}
	}

	steps := map[string][]string{"customers": {"customers"}, "invoices": {"customers", "invoices"},
		"payments": {"invoices", "payments"}, "full": {"customers", "invoices", "payments"}}[job.Kind]
	for _, step := range steps {
		var err error
		switch step {
		case "customers":
			err = pageLoop(func(start int) (int, error) {
				list, err := prov.Customers(ctx, sess, start, w.PageSize)
				if err != nil {
					return 0, err
				}
				return len(list), im.customers(ctx, list)
			})
		case "invoices":
			err = pageLoop(func(start int) (int, error) {
				list, err := prov.Invoices(ctx, sess, start, w.PageSize)
				if err != nil {
					return 0, err
				}
				return len(list), im.invoices(ctx, list)
			})
		case "payments":
			err = pageLoop(func(start int) (int, error) {
				list, err := prov.Payments(ctx, sess, start, w.PageSize)
				if err != nil {
					return 0, err
				}
				return len(list), im.payments(ctx, list)
			})
		}
		if err != nil {
			return fmt.Errorf("%s: %w", step, err)
		}
	}
	return nil
}

var errLeaseLost = errors.New("lease lost to another worker")

func (w *Worker) heartbeat(ctx context.Context, job store.SyncJob) error {
	var n int64
	err := tenant.WithTx(ctx, w.Pool, tenant.Scope{OrgID: job.OrganizationID}, func(tx pgx.Tx) error {
		var err error
		n, err = store.New(tx).ExtendSyncJobLease(ctx, store.ExtendSyncJobLeaseParams{ID: job.ID, LockedBy: &w.ID, LeaseSeconds: int32(w.Lease.Seconds())})
		return err
	})
	if err == nil && n == 0 {
		return errLeaseLost
	}
	return err
}

func classify(err error) (retry bool) {
	var p permanentError
	if errors.As(err, &p) {
		return false
	}
	var api *quickbooks.APIError
	if errors.As(err, &api) {
		return false
	}
	return true
}

func (w *Worker) finish(ctx context.Context, c claimed, job store.SyncJob, conn store.SyncConnection, stats *Stats, started time.Time, runErr error, log *slog.Logger) {
	if errors.Is(runErr, errLeaseLost) {
		log.Warn("abandoning job: lease taken over by another worker")
		return
	}
	statsJSON, _ := json.Marshal(stats)
	ctx = context.WithoutCancel(ctx)
	err := tenant.WithTx(ctx, w.Pool, tenant.Scope{OrgID: c.OrgID}, func(tx pgx.Tx) error {
		q := store.New(tx)
		attempt := store.InsertSyncJobAttemptParams{OrganizationID: c.OrgID, JobID: c.JobID, Attempt: c.Attempt, Worker: w.ID,
			StartedAt: started, Stats: statsJSON}
		var n int64
		var err error
		switch {
		case runErr == nil:
			n, err = q.CompleteSyncJob(ctx, store.CompleteSyncJobParams{ID: c.JobID, LockedBy: &w.ID, Stats: statsJSON})
			attempt.Outcome = "succeeded"
			if err == nil && n > 0 && conn.ID != uuid.Nil {
				err = q.TouchConnectionSynced(ctx, conn.ID)
			}
		case classify(runErr) && c.Attempt < c.MaxAttempts:
			msg := runErr.Error()
			n, err = q.RetrySyncJobLater(ctx, store.RetrySyncJobLaterParams{ID: c.JobID, LockedBy: &w.ID, RunAt: time.Now().Add(w.Backoff(int(c.Attempt))),
				LastError: &msg, Stats: statsJSON})
			attempt.Outcome, attempt.Error = "retrying", &msg
		default:
			msg := runErr.Error()
			n, err = q.FailSyncJob(ctx, store.FailSyncJobParams{ID: c.JobID, LockedBy: &w.ID, LastError: &msg, Stats: statsJSON})
			attempt.Outcome, attempt.Error = "failed", &msg
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return errLeaseLost
		}
		if err := q.InsertSyncJobAttempt(ctx, attempt); err != nil {
			return err
		}
		if attempt.Outcome == "retrying" {
			return nil
		}
		return audit.Record(ctx, q, tenant.Scope{OrgID: c.OrgID}, audit.Entry{Action: "sync." + attempt.Outcome, EntityType: "sync_job", EntityID: c.JobID,
			Extra: map[string]any{"kind": job.Kind, "attempt": c.Attempt, "error": attempt.Error, "stats": stats}})
	})
	switch {
	case errors.Is(err, errLeaseLost):
		log.Warn("job finished after its lease expired; result discarded")
	case err != nil:
		log.Error("record job result", "err", err)
	case runErr != nil:
		log.Warn("sync job did not succeed", "err", runErr)
	default:
		log.Info("sync job succeeded", "pages", stats.Pages)
	}
}
