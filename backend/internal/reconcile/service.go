package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"finopshq/internal/apperr"
	"finopshq/internal/audit"
	"finopshq/internal/authz"
	"finopshq/internal/httpx"
	"finopshq/internal/input"
	"finopshq/internal/money"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

type Service struct{ Pool *pgxpool.Pool }

type RunSummary struct {
	RunID      uuid.UUID      `json:"run_id"`
	ByStatus   map[string]int `json:"by_status"`
	NewOpen    int            `json:"new_open"`
	Unchanged  int            `json:"unchanged"`
	Superseded int            `json:"superseded"`
}

// Run compares imported records with the books. It is safe to repeat: unchanged results keep
// their existing record (and any decision); changed results supersede the old record.
func (s *Service) Run(ctx context.Context, scope tenant.Scope, trigger string) (RunSummary, error) {
	sum := RunSummary{ByStatus: map[string]int{}}
	err := tenant.WithTx(ctx, s.Pool, scope, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.LockReconciliation(ctx, scope.OrgID.String()); err != nil {
			return err
		}
		var startedBy *uuid.UUID
		if scope.UserID != uuid.Nil {
			startedBy = &scope.UserID
		}
		run, err := q.CreateReconciliationRun(ctx, store.CreateReconciliationRunParams{OrganizationID: scope.OrgID, Trigger: trigger, StartedBy: startedBy})
		if err != nil {
			return err
		}
		sum.RunID = run.ID

		invRows, err := q.ReconInternalInvoices(ctx)
		if err != nil {
			return err
		}
		internal := make([]InternalInvoice, 0, len(invRows))
		for _, r := range invRows {
			in := InternalInvoice{ID: r.ID, Number: r.Number, Date: r.IssueDate, Total: r.Total, Currency: r.Currency, CustomerName: r.CustomerName}
			if r.Source == "quickbooks" {
				in.ExternalID = r.ExternalID
			}
			internal = append(internal, in)
		}
		payRows, err := q.ReconInternalPayments(ctx)
		if err != nil {
			return err
		}
		intPays := make([]InternalPayment, 0, len(payRows))
		for _, p := range payRows {
			intPays = append(intPays, InternalPayment{ID: p.ID, InvoiceID: p.InvoiceID, Amount: p.Amount, Date: p.PaidOn, Currency: p.Currency})
		}
		extRows, err := q.ReconExternalRecords(ctx)
		if err != nil {
			return err
		}
		var extInv []ExternalInvoice
		var extPay []ExternalPayment
		for _, e := range extRows {
			if e.TxnDate == nil || e.Amount == nil || e.Currency == nil {
				continue
			}
			switch e.EntityType {
			case "invoice":
				extInv = append(extInv, ExternalInvoice{RecordID: e.ID, ExternalID: e.ExternalID, DocNumber: str(e.DocNumber), Date: *e.TxnDate,
					Amount: *e.Amount, Currency: *e.Currency, CustomerName: str(e.DisplayName)})
			case "payment":
				extPay = append(extPay, ExternalPayment{RecordID: e.ID, ExternalID: e.ExternalID, InvoiceExternalID: str(e.InvoiceExternalID),
					Date: *e.TxnDate, Amount: *e.Amount, Currency: *e.Currency})
			}
		}

		results := Compare(internal, intPays, extInv, extPay)
		current, err := q.CurrentReconciliationRecords(ctx)
		if err != nil {
			return err
		}
		byKey := make(map[string]store.ReconciliationRecord, len(current))
		for _, c := range current {
			byKey[c.MatchKey] = c
		}
		seen := map[string]bool{}
		for _, r := range results {
			seen[r.Key] = true
			sum.ByStatus[r.Status]++
			fp := r.Fingerprint()
			if cur, ok := byKey[r.Key]; ok {
				if bytes.Equal(cur.Fingerprint, fp) {
					sum.Unchanged++
					if err := q.TouchReconciliationRecord(ctx, store.TouchReconciliationRecordParams{ID: cur.ID, RunID: run.ID}); err != nil {
						return err
					}
					continue
				}
				if err := q.SupersedeReconciliationRecord(ctx, cur.ID); err != nil {
					return err
				}
				sum.Superseded++
			}
			review := "open"
			if r.Status == Matched {
				review = "not_required"
			} else {
				sum.NewOpen++
			}
			if err := q.InsertReconciliationRecord(ctx, store.InsertReconciliationRecordParams{
				OrganizationID: scope.OrgID, RunID: run.ID, EntityType: r.EntityType, MatchKey: r.Key, Fingerprint: fp, MatchStatus: r.Status,
				ExternalRecordID: r.ExternalRecord, InvoiceID: r.InvoiceID, PaymentID: r.PaymentID, Reference: strPtr(r.Reference),
				Counterparty: strPtr(r.Counterparty), Currency: strPtr(r.Currency), ExternalAmount: r.ExternalAmount, InternalAmount: r.InternalAmount,
				Difference: r.Difference(), ExternalDate: r.ExternalDate, InternalDate: r.InternalDate, ReviewStatus: review,
			}); err != nil {
				return err
			}
		}
		for key, cur := range byKey {
			if !seen[key] {
				if err := q.SupersedeReconciliationRecord(ctx, cur.ID); err != nil {
					return err
				}
				sum.Superseded++
			}
		}
		counts, _ := json.Marshal(sum)
		if err := q.FinishReconciliationRun(ctx, store.FinishReconciliationRunParams{ID: run.ID, Counts: counts}); err != nil {
			return err
		}
		return audit.Record(ctx, q, scope, audit.Entry{Action: "reconciliation.run", EntityType: "reconciliation_run", EntityID: run.ID,
			Extra: map[string]any{"trigger": trigger, "summary": sum}})
	})
	return sum, err
}

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type Record struct {
	ID               uuid.UUID  `json:"id"`
	EntityType       string     `json:"entity_type"`
	MatchStatus      string     `json:"match_status"`
	ReviewStatus     string     `json:"review_status"`
	Decision         *string    `json:"decision"`
	DecisionNote     *string    `json:"decision_note"`
	ReviewedByName   *string    `json:"reviewed_by_name"`
	ReviewedAt       *time.Time `json:"reviewed_at"`
	Reference        *string    `json:"reference"`
	Counterparty     *string    `json:"counterparty"`
	Currency         *string    `json:"currency"`
	ExternalAmount   *string    `json:"external_amount"`
	InternalAmount   *string    `json:"internal_amount"`
	Difference       *string    `json:"difference"`
	ExternalDate     *string    `json:"external_date"`
	InternalDate     *string    `json:"internal_date"`
	InvoiceID        *uuid.UUID `json:"invoice_id"`
	PaymentID        *uuid.UUID `json:"payment_id"`
	ExternalRecordID *uuid.UUID `json:"external_record_id"`
	AllowedDecisions []string   `json:"allowed_decisions"`
	CreatedAt        time.Time  `json:"created_at"`
}

func allowed(entity, status, review string, decision *string) []string {
	if review == "superseded" || review == "not_required" || (decision != nil && *decision != "follow_up") {
		return []string{}
	}
	switch status {
	case AmountMismatch, DateMismatch, MissingExternal:
		return []string{"accepted", "follow_up", "ignored"}
	case MissingInternal:
		if entity == "invoice" {
			return []string{"imported", "follow_up", "ignored"}
		}
		return []string{"follow_up", "ignored"}
	}
	return []string{}
}

func recordOf(r store.ReconciliationRecord, reviewer *string) Record {
	cur := str(r.Currency)
	f := func(d *decimal.Decimal) *string {
		if d == nil {
			return nil
		}
		v := money.Format(*d, cur)
		return &v
	}
	return Record{ID: r.ID, EntityType: r.EntityType, MatchStatus: r.MatchStatus, ReviewStatus: r.ReviewStatus, Decision: r.Decision,
		DecisionNote: r.DecisionNote, ReviewedByName: reviewer, ReviewedAt: r.ReviewedAt, Reference: r.Reference, Counterparty: r.Counterparty,
		Currency: r.Currency, ExternalAmount: f(r.ExternalAmount), InternalAmount: f(r.InternalAmount), Difference: f(r.Difference),
		ExternalDate: input.FormatDatePtr(r.ExternalDate), InternalDate: input.FormatDatePtr(r.InternalDate), InvoiceID: r.InvoiceID,
		PaymentID: r.PaymentID, ExternalRecordID: r.ExternalRecordID, AllowedDecisions: allowed(r.EntityType, r.MatchStatus, r.ReviewStatus, r.Decision),
		CreatedAt: r.CreatedAt}
}

func fromListRow(r store.ListReconciliationRecordsRow) Record {
	return recordOf(store.ReconciliationRecord{ID: r.ID, OrganizationID: r.OrganizationID, RunID: r.RunID, EntityType: r.EntityType, MatchKey: r.MatchKey,
		Fingerprint: r.Fingerprint, MatchStatus: r.MatchStatus, ExternalRecordID: r.ExternalRecordID, InvoiceID: r.InvoiceID, PaymentID: r.PaymentID,
		Reference: r.Reference, Counterparty: r.Counterparty, Currency: r.Currency, ExternalAmount: r.ExternalAmount, InternalAmount: r.InternalAmount,
		Difference: r.Difference, ExternalDate: r.ExternalDate, InternalDate: r.InternalDate, ReviewStatus: r.ReviewStatus, Decision: r.Decision,
		DecisionNote: r.DecisionNote, ReviewedBy: r.ReviewedBy, ReviewedAt: r.ReviewedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}, r.ReviewedByName)
}

type ListResult struct {
	Data    []Record                  `json:"data"`
	Total   int64                     `json:"total"`
	Summary map[string]map[string]int `json:"summary"`
	LastRun *time.Time                `json:"last_run"`
}

func (s *Service) List(ctx context.Context, p authz.Principal, f store.ListReconciliationRecordsParams) (ListResult, error) {
	if err := p.Require(authz.ReconcileView); err != nil {
		return ListResult{}, err
	}
	res := ListResult{Data: []Record{}, Summary: map[string]map[string]int{}}
	err := tenant.WithTx(ctx, s.Pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		rows, err := q.ListReconciliationRecords(ctx, f)
		if err != nil {
			return err
		}
		for _, r := range rows {
			res.Data = append(res.Data, fromListRow(r))
		}
		res.Total, err = q.CountReconciliationRecords(ctx, store.CountReconciliationRecordsParams{ReviewStatus: f.ReviewStatus,
			MatchStatus: f.MatchStatus, EntityType: f.EntityType, IncludeSuperseded: f.IncludeSuperseded})
		if err != nil {
			return err
		}
		sums, err := q.ReconciliationSummary(ctx)
		if err != nil {
			return err
		}
		for _, s := range sums {
			if res.Summary[s.MatchStatus] == nil {
				res.Summary[s.MatchStatus] = map[string]int{}
			}
			res.Summary[s.MatchStatus][s.ReviewStatus] = int(s.N)
		}
		if run, err := q.LatestReconciliationRun(ctx); err == nil {
			res.LastRun = run.FinishedAt
		} else if !apperr.IsNoRows(err) {
			return err
		}
		return nil
	})
	return res, err
}

type Detail struct {
	Record   Record          `json:"record"`
	History  []Record        `json:"history"`
	External json.RawMessage `json:"external_payload"`
}

func (s *Service) Get(ctx context.Context, p authz.Principal, id uuid.UUID) (Detail, error) {
	if err := p.Require(authz.ReconcileView); err != nil {
		return Detail{}, err
	}
	var d Detail
	err := tenant.WithTx(ctx, s.Pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		r, err := q.GetReconciliationRecordForUpdate(ctx, id)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("reconciliation record")
		}
		if err != nil {
			return err
		}
		hist, err := q.ReconciliationHistory(ctx, r.MatchKey)
		if err != nil {
			return err
		}
		for _, h := range hist {
			rec := recordOf(store.ReconciliationRecord{ID: h.ID, EntityType: h.EntityType, MatchStatus: h.MatchStatus, ReviewStatus: h.ReviewStatus,
				Decision: h.Decision, DecisionNote: h.DecisionNote, ReviewedAt: h.ReviewedAt, Reference: h.Reference, Counterparty: h.Counterparty,
				Currency: h.Currency, ExternalAmount: h.ExternalAmount, InternalAmount: h.InternalAmount, Difference: h.Difference,
				ExternalDate: h.ExternalDate, InternalDate: h.InternalDate, InvoiceID: h.InvoiceID, PaymentID: h.PaymentID,
				ExternalRecordID: h.ExternalRecordID, CreatedAt: h.CreatedAt}, h.ReviewedByName)
			if h.ID == id {
				d.Record = rec
			}
			d.History = append(d.History, rec)
		}
		if r.ExternalRecordID != nil {
			ext, err := q.GetExternalRecord(ctx, *r.ExternalRecordID)
			if err != nil {
				return err
			}
			d.External = ext.Raw
		}
		return nil
	})
	return d, err
}

// Review records a decision. "imported" creates the missing internal invoice from the QuickBooks
// record; every other decision only documents the outcome.
func (s *Service) Review(ctx context.Context, p authz.Principal, id uuid.UUID, decision, note string) (Record, error) {
	if err := p.Require(authz.ReconcileReview); err != nil {
		return Record{}, err
	}
	note = strings.TrimSpace(note)
	var out Record
	err := tenant.WithTx(ctx, s.Pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		r, err := q.GetReconciliationRecordForUpdate(ctx, id)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("reconciliation record")
		}
		if err != nil {
			return err
		}
		ok := false
		for _, a := range allowed(r.EntityType, r.MatchStatus, r.ReviewStatus, r.Decision) {
			if a == decision {
				ok = true
			}
		}
		if !ok {
			return apperr.Unprocessable("invalid_decision", fmt.Sprintf("%q is not an allowed decision for this record", decision))
		}
		if decision != "imported" && note == "" {
			return (&apperr.Validation{}).With("note", "explain the decision for the audit trail")
		}
		if len(note) > 1000 {
			return (&apperr.Validation{}).With("note", "is too long (max 1000 characters)")
		}
		var invoiceID *uuid.UUID
		if decision == "imported" {
			id, err := s.importInvoice(ctx, q, p, r)
			if err != nil {
				return err
			}
			invoiceID = &id
			if note == "" {
				note = "Created from the QuickBooks record"
			}
		}
		updated, err := q.DecideReconciliationRecord(ctx, store.DecideReconciliationRecordParams{ID: id, Decision: &decision, DecisionNote: &note,
			ReviewedBy: &p.User.ID, InvoiceID: invoiceID})
		if err != nil {
			return err
		}
		out = recordOf(updated, &p.User.Name)
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "reconciliation.decided", EntityType: "reconciliation_record", EntityID: id,
			Before: map[string]any{"review_status": r.ReviewStatus, "decision": r.Decision},
			After:  map[string]any{"decision": decision, "note": note, "match_status": r.MatchStatus, "reference": r.Reference, "invoice_id": invoiceID}})
	})
	return out, err
}

func (s *Service) importInvoice(ctx context.Context, q *store.Queries, p authz.Principal, r store.ReconciliationRecord) (uuid.UUID, error) {
	if r.ExternalRecordID == nil {
		return uuid.Nil, apperr.Unprocessable("no_external", "this record has no QuickBooks data to import")
	}
	ext, err := q.GetExternalRecord(ctx, *r.ExternalRecordID)
	if err != nil {
		return uuid.Nil, err
	}
	if ext.CustomerExternalID == nil || ext.Amount == nil || ext.TxnDate == nil || ext.Currency == nil {
		return uuid.Nil, apperr.Unprocessable("incomplete_external", "the QuickBooks record is missing data needed to create an invoice")
	}
	cust, err := q.CustomerByLinkedExternalID(ctx, ext.CustomerExternalID)
	if apperr.IsNoRows(err) {
		return uuid.Nil, apperr.Unprocessable("customer_not_imported", "the QuickBooks customer has not been imported yet; run a customer sync first")
	}
	if err != nil {
		return uuid.Nil, err
	}
	if !money.Supported(*ext.Currency) {
		return uuid.Nil, apperr.Unprocessable("unsupported_currency", "the invoice currency is not supported")
	}
	number := str(ext.DocNumber)
	if number == "" {
		number = "QB-" + ext.ExternalID
	}
	if taken, err := q.InvoiceNumberExists(ctx, store.InvoiceNumberExistsParams{OrganizationID: p.Org.ID, Number: number}); err != nil {
		return uuid.Nil, err
	} else if taken {
		number = "QB-" + number
	}
	due := *ext.TxnDate
	if ext.DueDate != nil && !ext.DueDate.Before(due) {
		due = *ext.DueDate
	}
	amount := *ext.Amount
	notes := "Imported from QuickBooks during reconciliation. The total is as reported by QuickBooks, tax included."
	inv, err := q.CreateInvoice(ctx, store.CreateInvoiceParams{OrganizationID: p.Org.ID, CustomerID: cust.ID, Number: number, Status: "sent",
		Currency: *ext.Currency, IssueDate: *ext.TxnDate, DueDate: due, DiscountType: "none", Subtotal: amount, Total: amount,
		Notes: &notes, Source: "quickbooks", ExternalID: &ext.ExternalID, CreatedBy: &p.User.ID})
	if err != nil {
		return uuid.Nil, err
	}
	if err := q.InsertInvoiceItem(ctx, store.InsertInvoiceItemParams{OrganizationID: p.Org.ID, InvoiceID: inv.ID, Position: 1,
		Description: "QuickBooks invoice " + str(ext.DocNumber), Quantity: decimal.NewFromInt(1), UnitPrice: amount, LineTotal: amount}); err != nil {
		return uuid.Nil, err
	}
	return inv.ID, audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "invoice.imported", EntityType: "invoice", EntityID: inv.ID,
		After: map[string]any{"number": number, "total": money.Format(amount, *ext.Currency), "currency": *ext.Currency, "external_id": ext.ExternalID}})
}

type Handler struct{ svc *Service }

func NewHandler(s *Service) *Handler { return &Handler{svc: s} }

func (h *Handler) Routes(r chi.Router) {
	r.Get("/", httpx.Handle(func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		review, err := input.QueryEnum(r, "review_status", "open", "reviewed", "not_required", "superseded")
		if err != nil {
			return 0, nil, err
		}
		match, err := input.QueryEnum(r, "match_status", Matched, AmountMismatch, DateMismatch, MissingInternal, MissingExternal)
		if err != nil {
			return 0, nil, err
		}
		entity, err := input.QueryEnum(r, "entity_type", "invoice", "payment")
		if err != nil {
			return 0, nil, err
		}
		var superseded *bool
		if review != nil && *review == "superseded" {
			t := true
			superseded = &t
		}
		page := input.PageFrom(r)
		res, err := h.svc.List(r.Context(), authz.MustPrincipal(r.Context()), store.ListReconciliationRecordsParams{ReviewStatus: review,
			MatchStatus: match, EntityType: entity, IncludeSuperseded: superseded, RowLimit: page.Limit, RowOffset: page.Offset})
		return http.StatusOK, res, err
	}))
	r.Post("/run", httpx.Handle(func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		p := authz.MustPrincipal(r.Context())
		if err := p.Require(authz.ReconcileReview); err != nil {
			return 0, nil, err
		}
		res, err := h.svc.Run(r.Context(), p.Scope(), "manual")
		return http.StatusOK, res, err
	}))
	r.Get("/{id}", httpx.Handle(func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		id, err := httpx.URLParamUUID(r, "id")
		if err != nil {
			return 0, nil, err
		}
		d, err := h.svc.Get(r.Context(), authz.MustPrincipal(r.Context()), id)
		return http.StatusOK, d, err
	}))
	r.Post("/{id}/review", httpx.Handle(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		id, err := httpx.URLParamUUID(r, "id")
		if err != nil {
			return 0, nil, err
		}
		in, err := httpx.Decode[struct {
			Decision string `json:"decision"`
			Note     string `json:"note"`
		}](w, r)
		if err != nil {
			return 0, nil, err
		}
		rec, err := h.svc.Review(r.Context(), authz.MustPrincipal(r.Context()), id, in.Decision, in.Note)
		return http.StatusOK, map[string]any{"record": rec}, err
	}))
}
