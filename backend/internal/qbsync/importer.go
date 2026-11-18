// Package qbsync connects organizations to QuickBooks and runs the background import jobs.
package qbsync

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"finopshq/internal/apperr"
	"finopshq/internal/audit"
	"finopshq/internal/money"
	"finopshq/internal/quickbooks"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

type EntityStats struct {
	Fetched   int `json:"fetched"`
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Rejected  int `json:"rejected"`
	Linked    int `json:"linked,omitempty"`
	Imported  int `json:"imported,omitempty"`
}

type Stats struct {
	Customers *EntityStats `json:"customers,omitempty"`
	Invoices  *EntityStats `json:"invoices,omitempty"`
	Payments  *EntityStats `json:"payments,omitempty"`
	Pages     int          `json:"pages"`
	Rejected  []string     `json:"rejected_examples,omitempty"`
	Reconcile any          `json:"reconciliation,omitempty"`
}

func (s *Stats) reject(e *EntityStats, err error) {
	e.Rejected++
	if len(s.Rejected) < 10 {
		s.Rejected = append(s.Rejected, err.Error())
	}
}

func hashOf(parts ...string) []byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return h.Sum(nil)
}

func datePtr(t time.Time) *time.Time { return &t }

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type importer struct {
	pool  *pgxpool.Pool
	scope tenant.Scope
	conn  store.SyncConnection
	jobID uuid.UUID
	stats *Stats
}

func (im *importer) upsert(ctx context.Context, q *store.Queries, e *EntityStats, p store.UpsertExternalRecordParams) error {
	p.OrganizationID, p.ConnectionID, p.JobID = im.scope.OrgID, im.conn.ID, &im.jobID
	inserted, err := q.UpsertExternalRecord(ctx, p)
	switch {
	case apperr.IsNoRows(err):
		e.Unchanged++
	case err != nil:
		return err
	case inserted:
		e.Created++
	default:
		e.Updated++
	}
	return nil
}

func (im *importer) customers(ctx context.Context, list []quickbooks.Customer) error {
	if im.stats.Customers == nil {
		im.stats.Customers = &EntityStats{}
	}
	e := im.stats.Customers
	e.Fetched += len(list)
	return tenant.WithTx(ctx, im.pool, im.scope, func(tx pgx.Tx) error {
		q := store.New(tx)
		for _, c := range list {
			if err := quickbooks.ValidateCustomer(c); err != nil {
				im.stats.reject(e, err)
				continue
			}
			if !money.Supported(c.Currency) {
				im.stats.reject(e, fmt.Errorf("customer %s uses unsupported currency %s", c.ID, c.Currency))
				continue
			}
			if err := im.upsert(ctx, q, e, store.UpsertExternalRecordParams{EntityType: "customer", ExternalID: c.ID,
				DisplayName: strPtr(c.DisplayName), Currency: strPtr(c.Currency), Raw: c.Raw,
				PayloadHash: hashOf(string(c.Raw)), ExternalUpdatedAt: c.UpdatedAt}); err != nil {
				return err
			}
			if err := im.linkCustomer(ctx, q, c); err != nil {
				return err
			}
		}
		return nil
	})
}

// linkCustomer keeps internal customers in step with QuickBooks without overwriting what the
// team entered: an existing link wins, then an exact name match is linked, otherwise a new
// customer is created with source = 'quickbooks'.
func (im *importer) linkCustomer(ctx context.Context, q *store.Queries, c quickbooks.Customer) error {
	if _, err := q.CustomerByLinkedExternalID(ctx, &c.ID); err == nil {
		return nil
	} else if !apperr.IsNoRows(err) {
		return err
	}
	if existing, err := q.FindCustomerByName(ctx, c.DisplayName); err == nil {
		if err := q.LinkCustomerExternal(ctx, store.LinkCustomerExternalParams{ID: existing.ID, ExternalID: &c.ID}); err != nil {
			return err
		}
		im.stats.Customers.Linked++
		return nil
	} else if !apperr.IsNoRows(err) {
		return err
	}
	status := "active"
	if !c.Active {
		status = "inactive"
	}
	created, err := q.CreateCustomer(ctx, store.CreateCustomerParams{OrganizationID: im.scope.OrgID, Name: c.DisplayName, Email: strPtr(c.Email),
		Phone: strPtr(c.Phone), AddressLine1: strPtr(c.Line1), City: strPtr(c.City), Region: strPtr(c.Region), PostalCode: strPtr(c.PostalCode),
		Country: strPtr(c.Country), Currency: c.Currency, Status: status, Source: "quickbooks", ExternalID: &c.ID})
	if err != nil {
		return err
	}
	im.stats.Customers.Imported++
	return audit.Record(ctx, q, im.scope, audit.Entry{Action: "customer.imported", EntityType: "customer", EntityID: created.ID,
		After: map[string]any{"name": created.Name, "source": "quickbooks", "external_id": c.ID}})
}

func (im *importer) invoices(ctx context.Context, list []quickbooks.Invoice) error {
	if im.stats.Invoices == nil {
		im.stats.Invoices = &EntityStats{}
	}
	e := im.stats.Invoices
	e.Fetched += len(list)
	return tenant.WithTx(ctx, im.pool, im.scope, func(tx pgx.Tx) error {
		q := store.New(tx)
		for _, inv := range list {
			if err := quickbooks.ValidateInvoice(inv); err != nil {
				im.stats.reject(e, err)
				continue
			}
			if !money.Supported(inv.Currency) || !money.HasAtMostPlaces(inv.Total, money.MinorUnits(inv.Currency)) {
				im.stats.reject(e, fmt.Errorf("invoice %s has an amount or currency this system cannot store", inv.ID))
				continue
			}
			total, bal := inv.Total, inv.Balance
			if err := im.upsert(ctx, q, e, store.UpsertExternalRecordParams{EntityType: "invoice", ExternalID: inv.ID, DocNumber: strPtr(inv.DocNumber),
				DisplayName: strPtr(inv.CustomerName), CustomerExternalID: strPtr(inv.CustomerID), TxnDate: datePtr(inv.TxnDate), DueDate: inv.DueDate,
				Amount: &total, Balance: &bal, Currency: strPtr(inv.Currency), Raw: inv.Raw,
				PayloadHash: hashOf(string(inv.Raw), inv.Total.String(), inv.Balance.String(), inv.TxnDate.String()), ExternalUpdatedAt: inv.UpdatedAt}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (im *importer) payments(ctx context.Context, list []quickbooks.Payment) error {
	if im.stats.Payments == nil {
		im.stats.Payments = &EntityStats{}
	}
	e := im.stats.Payments
	e.Fetched += len(list)
	return tenant.WithTx(ctx, im.pool, im.scope, func(tx pgx.Tx) error {
		q := store.New(tx)
		for _, p := range list {
			if err := quickbooks.ValidatePayment(p); err != nil {
				im.stats.reject(e, err)
				continue
			}
			apps := p.Applied
			if len(apps) == 0 {
				apps = []quickbooks.PaymentApplication{{Amount: p.Total}}
			}
			// One staged record per invoice the payment is applied to, so each can be reconciled.
			for _, a := range apps {
				amount := a.Amount
				extID := p.ID
				if a.InvoiceID != "" {
					extID = p.ID + ":" + a.InvoiceID
				}
				if err := im.upsert(ctx, q, e, store.UpsertExternalRecordParams{EntityType: "payment", ExternalID: extID,
					CustomerExternalID: strPtr(p.CustomerID), InvoiceExternalID: strPtr(a.InvoiceID), TxnDate: datePtr(p.TxnDate),
					Amount: &amount, Currency: strPtr(p.Currency), Raw: p.Raw,
					PayloadHash: hashOf(string(p.Raw), amount.String(), a.InvoiceID), ExternalUpdatedAt: p.UpdatedAt}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// SnapshotFunc feeds the simulated provider from the organization's own records.
func SnapshotFunc(pool *pgxpool.Pool) func(ctx context.Context, orgID uuid.UUID) (quickbooks.Snapshot, error) {
	return func(ctx context.Context, orgID uuid.UUID) (quickbooks.Snapshot, error) {
		var snap quickbooks.Snapshot
		err := tenant.WithTx(ctx, pool, tenant.Scope{OrgID: orgID}, func(tx pgx.Tx) error {
			q := store.New(tx)
			org, err := q.GetOrganization(ctx, orgID)
			if err != nil {
				return err
			}
			loc, err := time.LoadLocation(org.Timezone)
			if err != nil {
				loc = time.UTC
			}
			now := time.Now().In(loc)
			snap.Today = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			cs, err := q.SyncSnapshotCustomers(ctx)
			if err != nil {
				return err
			}
			for _, c := range cs {
				snap.Customers = append(snap.Customers, quickbooks.SnapCustomer{ID: c.ID, Name: c.Name, Email: c.Email, Currency: c.Currency})
			}
			is, err := q.SyncSnapshotInvoices(ctx)
			if err != nil {
				return err
			}
			for _, i := range is {
				snap.Invoices = append(snap.Invoices, quickbooks.SnapInvoice{ID: i.ID, Number: i.Number, CustomerID: i.CustomerID, CustomerName: i.CustomerName,
					IssueDate: i.IssueDate, DueDate: i.DueDate, Total: i.Total, AmountPaid: i.AmountPaid, Currency: i.Currency})
			}
			ps, err := q.SyncSnapshotPayments(ctx)
			if err != nil {
				return err
			}
			for _, p := range ps {
				snap.Payments = append(snap.Payments, quickbooks.SnapPayment{ID: p.ID, InvoiceID: p.InvoiceID, InvoiceNumber: p.InvoiceNumber,
					Amount: p.Amount, PaidOn: p.PaidOn, Currency: p.Currency})
			}
			return nil
		})
		return snap, err
	}
}
