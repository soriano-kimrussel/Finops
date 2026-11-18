package invoices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"finopshq/internal/apperr"
	"finopshq/internal/audit"
	"finopshq/internal/authz"
	"finopshq/internal/input"
	"finopshq/internal/ledger"
	"finopshq/internal/money"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

var paymentMethods = []string{"bank_transfer", "card", "cash", "check", "other"}

type PaymentInput struct {
	Amount    string  `json:"amount"`
	PaidOn    string  `json:"paid_on"`
	Method    string  `json:"method"`
	Reference *string `json:"reference"`
}

type PaymentResult struct {
	Payment  Payment `json:"payment"`
	Invoice  Invoice `json:"invoice"`
	Replayed bool    `json:"replayed"`
}

func fingerprint(invoiceID uuid.UUID, amount decimal.Decimal, paidOn, method string, reference *string) []byte {
	ref := ""
	if reference != nil {
		ref = *reference
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%s", invoiceID, amount.String(), paidOn, method, ref)))
	return sum[:]
}

// RecordPayment applies a payment to an invoice. The invoice row is locked first so concurrent
// payments are serialized; the idempotency key is checked after the lock, so a retried request
// always sees the original attempt and gets the same result back instead of paying twice.
func (s *Service) RecordPayment(ctx context.Context, p authz.Principal, invoiceID uuid.UUID, key string, in PaymentInput) (PaymentResult, error) {
	if err := p.Require(authz.PaymentsRecord); err != nil {
		return PaymentResult{}, err
	}
	key = strings.TrimSpace(key)
	if len(key) < 8 || len(key) > 100 {
		return PaymentResult{}, apperr.BadRequest("an Idempotency-Key header of 8 to 100 characters is required")
	}

	var res PaymentResult
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		inv, err := q.GetInvoiceForUpdate(ctx, invoiceID)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("invoice")
		}
		if err != nil {
			return err
		}

		var v apperr.Validation
		amount, aerr := money.ParseAmount(in.Amount, inv.Currency)
		if aerr != nil {
			v.Add("amount", aerr.Error())
		}
		paidOn := input.Date(&v, "paid_on", in.PaidOn)
		if !v.Has("paid_on") && paidOn.After(p.Today()) {
			v.Add("paid_on", "cannot be in the future")
		}
		method := strings.TrimSpace(in.Method)
		if !contains(paymentMethods, method) {
			v.Add("method", "must be one of bank_transfer, card, cash, check, other")
		}
		ref := input.OptionalText(&v, "reference", in.Reference, 100)

		fp := fingerprint(invoiceID, amount, in.PaidOn, method, ref)
		existing, err := q.GetPaymentByIdempotencyKey(ctx, store.GetPaymentByIdempotencyKeyParams{OrganizationID: p.Org.ID, IdempotencyKey: key})
		if err == nil {
			if existing.InvoiceID != invoiceID || !bytes.Equal(existing.RequestFingerprint, fp) {
				return apperr.Conflict("idempotency_conflict", "this Idempotency-Key was already used for a different payment")
			}
			res.Payment = paymentFromStore(existing)
			res.Replayed = true
			res.Invoice, err = load(ctx, q, invoiceID, p)
			return err
		}
		if !apperr.IsNoRows(err) {
			return err
		}
		if err := v.Err(); err != nil {
			return err
		}

		switch inv.Status {
		case "draft":
			return apperr.Unprocessable("invoice_not_sent", "send the invoice before recording payments against it")
		case "void":
			return apperr.Unprocessable("invoice_void", "payments cannot be recorded against a void invoice")
		case "paid":
			return apperr.Unprocessable("invoice_paid", "this invoice is already paid in full")
		}
		balance := inv.Total.Sub(inv.AmountPaid)
		if amount.GreaterThan(balance) {
			return &apperr.Error{Status: 422, Code: "overpayment",
				Message: fmt.Sprintf("payment of %s exceeds the outstanding balance of %s %s", money.Format(amount, inv.Currency), money.Format(balance, inv.Currency), inv.Currency),
				Fields:  map[string]string{"amount": "exceeds the outstanding balance of " + money.Format(balance, inv.Currency)}}
		}

		pay, err := q.InsertPayment(ctx, store.InsertPaymentParams{
			OrganizationID: p.Org.ID, InvoiceID: invoiceID, Amount: amount, Currency: inv.Currency, PaidOn: paidOn,
			Method: method, Reference: ref, IdempotencyKey: key, RequestFingerprint: fp, RecordedBy: &p.User.ID, Source: "internal",
		})
		if apperr.IsUniqueViolation(err, "payments_idempotency_key") {
			return apperr.Conflict("idempotency_conflict", "this Idempotency-Key is already in use")
		}
		if err != nil {
			return err
		}

		newPaid := inv.AmountPaid.Add(amount)
		status := "partially_paid"
		if newPaid.Equal(inv.Total) {
			status = "paid"
		}
		if _, err := q.SetInvoicePaid(ctx, store.SetInvoicePaidParams{ID: invoiceID, AmountPaid: newPaid, Status: status}); err != nil {
			return err
		}
		if err := ledger.Post(ctx, q, ledger.Entry{OrgID: p.Org.ID, OccurredOn: paidOn, Direction: "in", Amount: amount, Currency: inv.Currency,
			Description: "Payment for " + inv.Number, SourceType: "payment", SourceID: pay.ID}); err != nil {
			return err
		}
		res.Payment = paymentFromStore(pay)
		res.Invoice, err = load(ctx, q, invoiceID, p)
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "payment.recorded", EntityType: "payment", EntityID: pay.ID,
			After: map[string]any{"invoice_id": invoiceID, "invoice_number": inv.Number, "amount": res.Payment.Amount, "currency": inv.Currency,
				"paid_on": res.Payment.PaidOn, "method": method, "reference": ref},
			Extra: map[string]any{"invoice_status": map[string]string{"from": inv.Status, "to": status},
				"invoice_balance": map[string]string{"from": money.Format(balance, inv.Currency), "to": res.Invoice.Balance}}})
	})
	return res, err
}

// ReversePayment undoes a payment without deleting it: the payment is marked reversed, the invoice
// balance is restored and an outgoing ledger entry records the reversal.
func (s *Service) ReversePayment(ctx context.Context, p authz.Principal, paymentID uuid.UUID, reason string) (PaymentResult, error) {
	if err := p.Require(authz.PaymentsRecord); err != nil {
		return PaymentResult{}, err
	}
	var v apperr.Validation
	reason = input.Text(&v, "reason", reason, 500)
	if err := v.Err(); err != nil {
		return PaymentResult{}, err
	}
	var res PaymentResult
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		peek, err := q.GetPayment(ctx, paymentID)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("payment")
		}
		if err != nil {
			return err
		}
		inv, err := q.GetInvoiceForUpdate(ctx, peek.InvoiceID)
		if err != nil {
			return err
		}
		pay, err := q.GetPaymentForUpdate(ctx, paymentID)
		if err != nil {
			return err
		}
		if pay.Status != "completed" {
			return apperr.Unprocessable("already_reversed", "this payment has already been reversed")
		}
		newPaid := inv.AmountPaid.Sub(pay.Amount)
		status := "partially_paid"
		if newPaid.IsZero() {
			status = "sent"
		}
		reversed, err := q.ReversePayment(ctx, store.ReversePaymentParams{ID: paymentID, ReversedBy: &p.User.ID, ReversalReason: &reason})
		if err != nil {
			return err
		}
		if _, err := q.SetInvoicePaid(ctx, store.SetInvoicePaidParams{ID: inv.ID, AmountPaid: newPaid, Status: status}); err != nil {
			return err
		}
		if err := ledger.Post(ctx, q, ledger.Entry{OrgID: p.Org.ID, OccurredOn: p.Today(), Direction: "out", Amount: pay.Amount, Currency: pay.Currency,
			Description: "Reversal of payment for " + inv.Number, SourceType: "payment_reversal", SourceID: pay.ID}); err != nil {
			return err
		}
		res.Payment = paymentFromStore(reversed)
		res.Invoice, err = load(ctx, q, inv.ID, p)
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "payment.reversed", EntityType: "payment", EntityID: pay.ID,
			Before: map[string]any{"status": "completed"},
			After:  map[string]any{"status": "reversed", "reason": reason, "amount": money.Format(pay.Amount, pay.Currency)},
			Extra:  map[string]any{"invoice_id": inv.ID, "invoice_status": map[string]string{"from": inv.Status, "to": status}}})
	})
	return res, err
}

type PaymentList struct {
	Data  []Payment `json:"data"`
	Total int64     `json:"total"`
}

func (s *Service) ListPayments(ctx context.Context, p authz.Principal, f store.ListPaymentsParams) (PaymentList, error) {
	if err := p.Require(authz.InvoicesView); err != nil {
		return PaymentList{}, err
	}
	var res PaymentList
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		rows, err := q.ListPayments(ctx, f)
		if err != nil {
			return err
		}
		res.Total, err = q.CountPayments(ctx, store.CountPaymentsParams{From: f.From, To: f.To, Method: f.Method, Status: f.Status, Pattern: f.Pattern})
		if err != nil {
			return err
		}
		res.Data = make([]Payment, 0, len(rows))
		for _, r := range rows {
			cid := r.CustomerID
			res.Data = append(res.Data, Payment{ID: r.ID, InvoiceID: r.InvoiceID, InvoiceNumber: r.InvoiceNumber, CustomerID: &cid,
				CustomerName: r.CustomerName, Amount: money.Format(r.Amount, r.Currency), Currency: r.Currency, PaidOn: input.FormatDate(r.PaidOn),
				Method: r.Method, Reference: r.Reference, Status: r.Status, Source: r.Source, CreatedAt: r.CreatedAt})
		}
		return nil
	})
	return res, err
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
