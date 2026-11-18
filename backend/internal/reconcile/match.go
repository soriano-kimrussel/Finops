// Package reconcile compares records imported from QuickBooks with the internal books and keeps a
// reviewable, append-only history of the differences and of the decisions taken about them.
package reconcile

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const (
	Matched         = "matched"
	AmountMismatch  = "amount_mismatch"
	DateMismatch    = "date_mismatch"
	MissingInternal = "missing_internal" // in QuickBooks, not in FinOps HQ
	MissingExternal = "missing_external" // in FinOps HQ, not in QuickBooks
)

type InternalInvoice struct {
	ID           uuid.UUID
	Number       string
	Date         time.Time
	Total        decimal.Decimal
	Currency     string
	CustomerName string
	ExternalID   *string // set when the invoice was imported from QuickBooks
}

type InternalPayment struct {
	ID        uuid.UUID
	InvoiceID uuid.UUID
	Amount    decimal.Decimal
	Date      time.Time
	Currency  string
}

type ExternalInvoice struct {
	RecordID     uuid.UUID
	ExternalID   string
	DocNumber    string
	Date         time.Time
	Amount       decimal.Decimal
	Currency     string
	CustomerName string
}

type ExternalPayment struct {
	RecordID          uuid.UUID
	ExternalID        string
	InvoiceExternalID string
	Date              time.Time
	Amount            decimal.Decimal
	Currency          string
}

type Result struct {
	EntityType     string
	Status         string
	Key            string
	ExternalRecord *uuid.UUID
	InvoiceID      *uuid.UUID
	PaymentID      *uuid.UUID
	Reference      string
	Counterparty   string
	Currency       string
	ExternalAmount *decimal.Decimal
	InternalAmount *decimal.Decimal
	ExternalDate   *time.Time
	InternalDate   *time.Time
}

// Difference is external − internal when both sides exist.
func (r Result) Difference() *decimal.Decimal {
	if r.ExternalAmount == nil || r.InternalAmount == nil {
		return nil
	}
	d := r.ExternalAmount.Sub(*r.InternalAmount)
	return &d
}

// Fingerprint identifies the content of a result; a change produces a new reviewable record.
func (r Result) Fingerprint() []byte {
	s := fmt.Sprintf("%s|%s|%v|%v|%s|%s|%s|%s|%s", r.Status, r.Key, deref(r.ExternalRecord), deref(r.InvoiceID), deref(r.PaymentID),
		decStr(r.ExternalAmount), decStr(r.InternalAmount), dateStr(r.ExternalDate), dateStr(r.InternalDate))
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func deref(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func decStr(d *decimal.Decimal) string {
	if d == nil {
		return ""
	}
	return d.String()
}

func dateStr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

func ptr[T any](v T) *T { return &v }

func compare(extAmt, intAmt decimal.Decimal, extCur, intCur string, extDate, intDate time.Time) string {
	switch {
	case !extAmt.Equal(intAmt) || extCur != intCur:
		return AmountMismatch
	case !extDate.Equal(intDate):
		return DateMismatch
	default:
		return Matched
	}
}

// Compare matches the two sides. Invoices match by import link (external id) first, then by
// document number. Payments are matched within their (matched) invoice: an exact amount and
// date wins, then the same amount on another date, then the nearest-dated remaining payment.
func Compare(internal []InternalInvoice, intPays []InternalPayment, external []ExternalInvoice, extPays []ExternalPayment) []Result {
	var out []Result
	byNumber := map[string]int{}
	byExternal := map[string]int{}
	for i, inv := range internal {
		byNumber[inv.Number] = i
		if inv.ExternalID != nil {
			byExternal[*inv.ExternalID] = i
		}
	}
	matchedInternal := map[uuid.UUID]bool{}
	extToInternal := map[string]uuid.UUID{} // external invoice id -> internal invoice id

	sort.Slice(external, func(a, b int) bool { return external[a].ExternalID < external[b].ExternalID })
	for _, e := range external {
		r := Result{EntityType: "invoice", Key: "invoice:ext:" + e.ExternalID, ExternalRecord: ptr(e.RecordID), Reference: e.DocNumber,
			Counterparty: e.CustomerName, Currency: e.Currency, ExternalAmount: ptr(e.Amount), ExternalDate: ptr(e.Date)}
		idx, ok := byExternal[e.ExternalID]
		if !ok && e.DocNumber != "" {
			idx, ok = byNumber[e.DocNumber]
			if ok && matchedInternal[internal[idx].ID] {
				ok = false
			}
		}
		if !ok {
			r.Status = MissingInternal
			out = append(out, r)
			continue
		}
		in := internal[idx]
		matchedInternal[in.ID] = true
		extToInternal[e.ExternalID] = in.ID
		r.InvoiceID, r.InternalAmount, r.InternalDate = ptr(in.ID), ptr(in.Total), ptr(in.Date)
		r.Counterparty = in.CustomerName
		r.Status = compare(e.Amount, in.Total, e.Currency, in.Currency, e.Date, in.Date)
		out = append(out, r)
	}
	if len(external) > 0 {
		for _, in := range internal {
			if matchedInternal[in.ID] {
				continue
			}
			out = append(out, Result{EntityType: "invoice", Status: MissingExternal, Key: "invoice:int:" + in.ID.String(), InvoiceID: ptr(in.ID),
				Reference: in.Number, Counterparty: in.CustomerName, Currency: in.Currency, InternalAmount: ptr(in.Total), InternalDate: ptr(in.Date)})
		}
	}

	invByID := map[uuid.UUID]InternalInvoice{}
	for _, in := range internal {
		invByID[in.ID] = in
	}
	extDoc := map[string]ExternalInvoice{}
	for _, e := range external {
		extDoc[e.ExternalID] = e
	}
	label := func(r *Result, invID *uuid.UUID, extInvoiceID string) {
		if invID != nil {
			if in, ok := invByID[*invID]; ok {
				r.Reference, r.Counterparty = in.Number, in.CustomerName
				return
			}
		}
		if e, ok := extDoc[extInvoiceID]; ok {
			r.Reference, r.Counterparty = e.DocNumber, e.CustomerName
		}
	}

	paysByInvoice := map[uuid.UUID][]InternalPayment{}
	for _, p := range intPays {
		paysByInvoice[p.InvoiceID] = append(paysByInvoice[p.InvoiceID], p)
	}
	usedPayment := map[uuid.UUID]bool{}
	sort.Slice(extPays, func(a, b int) bool { return extPays[a].ExternalID < extPays[b].ExternalID })
	for _, e := range extPays {
		r := Result{EntityType: "payment", Key: "payment:ext:" + e.ExternalID, ExternalRecord: ptr(e.RecordID), Currency: e.Currency,
			ExternalAmount: ptr(e.Amount), ExternalDate: ptr(e.Date)}
		invID, ok := extToInternal[e.InvoiceExternalID]
		if !ok {
			label(&r, nil, e.InvoiceExternalID)
			r.Status = MissingInternal
			out = append(out, r)
			continue
		}
		r.InvoiceID = ptr(invID)
		label(&r, &invID, e.InvoiceExternalID)
		candidates := paysByInvoice[invID]
		best := -1
		rank := func(p InternalPayment) int {
			switch {
			case p.Amount.Equal(e.Amount) && p.Date.Equal(e.Date):
				return 0
			case p.Amount.Equal(e.Amount):
				return 1
			default:
				return 2
			}
		}
		for i, p := range candidates {
			if usedPayment[p.ID] {
				continue
			}
			if best < 0 || rank(p) < rank(candidates[best]) ||
				(rank(p) == rank(candidates[best]) && absDays(p.Date, e.Date) < absDays(candidates[best].Date, e.Date)) {
				best = i
			}
		}
		if best < 0 {
			r.Status = MissingInternal
			out = append(out, r)
			continue
		}
		p := candidates[best]
		usedPayment[p.ID] = true
		r.PaymentID, r.InternalAmount, r.InternalDate = ptr(p.ID), ptr(p.Amount), ptr(p.Date)
		r.Status = compare(e.Amount, p.Amount, e.Currency, p.Currency, e.Date, p.Date)
		out = append(out, r)
	}
	if len(extPays) > 0 {
		for _, p := range intPays {
			if usedPayment[p.ID] || !matchedInternal[p.InvoiceID] {
				continue
			}
			r := Result{EntityType: "payment", Status: MissingExternal, Key: "payment:int:" + p.ID.String(), PaymentID: ptr(p.ID),
				InvoiceID: ptr(p.InvoiceID), Currency: p.Currency, InternalAmount: ptr(p.Amount), InternalDate: ptr(p.Date)}
			label(&r, &p.InvoiceID, "")
			out = append(out, r)
		}
	}
	return out
}

func absDays(a, b time.Time) float64 {
	d := a.Sub(b).Hours() / 24
	if d < 0 {
		return -d
	}
	return d
}
