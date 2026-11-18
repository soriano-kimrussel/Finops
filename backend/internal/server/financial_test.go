package server_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"finopshq/internal/apitest"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

type invoiceBody struct {
	Invoice struct {
		ID            string `json:"id"`
		Number        string `json:"number"`
		Status        string `json:"status"`
		DisplayStatus string `json:"display_status"`
		IsOverdue     bool   `json:"is_overdue"`
		Subtotal      string `json:"subtotal"`
		DiscountTotal string `json:"discount_total"`
		TaxTotal      string `json:"tax_total"`
		Total         string `json:"total"`
		AmountPaid    string `json:"amount_paid"`
		Balance       string `json:"balance"`
		Version       int32  `json:"version"`
		Items         []struct {
			LineTotal string `json:"line_total"`
		} `json:"items"`
		Payments []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"payments"`
	} `json:"invoice"`
}

type books struct {
	env      *apitest.Env
	owner    *apitest.Client
	org      string
	customer string
}

func newBooks(t *testing.T, currency string) *books {
	env := apitest.New(t)
	c := env.Client()
	c.Register("Owner")
	org := c.CreateOrg("Books Ltd", currency)
	var cust struct {
		Customer struct{ ID string } `json:"customer"`
	}
	c.Must(http.StatusCreated, c.Post("/api/orgs/"+org+"/customers", map[string]any{"name": "Globex Corporation", "email": "ap@globex.example", "currency": currency})).JSON(t, &cust)
	return &books{env: env, owner: c, org: org, customer: cust.Customer.ID}
}

func (b *books) url(path string) string { return "/api/orgs/" + b.org + path }

func (b *books) invoice(t *testing.T, body map[string]any) invoiceBody {
	t.Helper()
	if _, ok := body["customer_id"]; !ok {
		body["customer_id"] = b.customer
	}
	if _, ok := body["issue_date"]; !ok {
		body["issue_date"] = time.Now().AddDate(0, 0, -5).Format("2006-01-02")
	}
	if _, ok := body["due_date"]; !ok {
		body["due_date"] = time.Now().AddDate(0, 0, 25).Format("2006-01-02")
	}
	var out invoiceBody
	b.owner.Must(http.StatusCreated, b.owner.Post(b.url("/invoices"), body)).JSON(t, &out)
	return out
}

func (b *books) sentInvoice(t *testing.T, unitPrice string) invoiceBody {
	t.Helper()
	inv := b.invoice(t, map[string]any{"items": []map[string]string{{"description": "Consulting", "quantity": "1", "unit_price": unitPrice}}})
	var out invoiceBody
	b.owner.Must(http.StatusOK, b.owner.Post(b.url("/invoices/"+inv.Invoice.ID+"/send"), nil)).JSON(t, &out)
	return out
}

func (b *books) pay(c *apitest.Client, invoiceID, amount, key string) apitest.Response {
	return c.Post(b.url("/invoices/"+invoiceID+"/payments"), map[string]string{
		"amount": amount, "paid_on": time.Now().Format("2006-01-02"), "method": "bank_transfer", "reference": "wire " + key,
	}, "Idempotency-Key", key)
}

func TestInvoiceTotalsAreCalculatedOnTheServer(t *testing.T) {
	b := newBooks(t, "USD")
	inv := b.invoice(t, map[string]any{
		"discount_type": "percent", "discount_value": "10", "tax_rate": "8.25",
		"items": []map[string]string{
			{"description": "Design sprint", "quantity": "3", "unit_price": "1250.00"},
			{"description": "Hosting", "quantity": "1.5", "unit_price": "19.99"},
		},
	})
	got := inv.Invoice
	if got.Number != "INV-000001" || got.Status != "draft" {
		t.Fatalf("unexpected header %+v", got)
	}
	if got.Items[1].LineTotal != "29.99" || got.Subtotal != "3779.99" || got.DiscountTotal != "378.00" || got.TaxTotal != "280.66" || got.Total != "3682.65" {
		t.Fatalf("unexpected totals %+v", got)
	}

	var preview struct {
		Total string `json:"total"`
	}
	b.owner.Must(http.StatusOK, b.owner.Post(b.url("/invoices/preview"), map[string]any{
		"discount_type": "fixed", "discount_value": "5.00", "tax_rate": "0",
		"items": []map[string]string{{"description": "x", "quantity": "2", "unit_price": "10.005"}},
	})).JSON(t, &preview)
	if preview.Total != "15.01" {
		t.Fatalf("preview total = %s", preview.Total)
	}

	second := b.invoice(t, map[string]any{"items": []map[string]string{{"description": "x", "quantity": "1", "unit_price": "1"}}})
	if second.Invoice.Number != "INV-000002" {
		t.Fatalf("numbers must be sequential, got %s", second.Invoice.Number)
	}
}

func TestInvoiceValidation(t *testing.T) {
	b := newBooks(t, "USD")
	res := b.owner.Post(b.url("/invoices"), map[string]any{
		"customer_id": b.customer, "issue_date": "2026-03-10", "due_date": "2026-03-01",
		"discount_type": "fixed", "discount_value": "500", "tax_rate": "150",
		"items": []map[string]string{{"description": "", "quantity": "0", "unit_price": "-1"}},
	})
	if res.Status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d %s", res.Status, res.Body)
	}
	fields := res.Map(t)["error"].(map[string]any)["fields"].(map[string]any)
	for _, f := range []string{"due_date", "tax_rate", "items.0.description", "items.0.quantity", "items.0.unit_price"} {
		if _, ok := fields[f]; !ok {
			t.Errorf("expected field error %s in %v", f, fields)
		}
	}
	res = b.owner.Post(b.url("/invoices"), map[string]any{"customer_id": b.customer, "issue_date": "2026-03-01", "due_date": "2026-03-01",
		"discount_type": "fixed", "discount_value": "20", "items": []map[string]string{{"description": "x", "quantity": "1", "unit_price": "10"}}})
	if res.Status != http.StatusUnprocessableEntity {
		t.Fatalf("discount larger than subtotal must be rejected, got %d", res.Status)
	}
}

func TestPartialAndFullPayments(t *testing.T) {
	b := newBooks(t, "USD")
	inv := b.sentInvoice(t, "1000.00").Invoice

	var r struct {
		Invoice  invoiceBody `json:"-"`
		Replayed bool        `json:"replayed"`
	}
	res := b.owner.Must(http.StatusCreated, b.pay(b.owner, inv.ID, "400.00", "pay-0001-aaaa"))
	res.JSON(t, &r)
	var after invoiceBody
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/invoices/"+inv.ID))).JSON(t, &after)
	if after.Invoice.Status != "partially_paid" || after.Invoice.AmountPaid != "400.00" || after.Invoice.Balance != "600.00" {
		t.Fatalf("after partial payment: %+v", after.Invoice)
	}

	over := b.pay(b.owner, inv.ID, "600.01", "pay-0002-bbbb")
	if over.Status != http.StatusUnprocessableEntity || over.ErrorCode(t) != "overpayment" {
		t.Fatalf("overpayment must be rejected, got %d %s", over.Status, over.Body)
	}

	b.owner.Must(http.StatusCreated, b.pay(b.owner, inv.ID, "600.00", "pay-0003-cccc"))
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/invoices/"+inv.ID))).JSON(t, &after)
	if after.Invoice.Status != "paid" || after.Invoice.Balance != "0.00" || len(after.Invoice.Payments) != 2 {
		t.Fatalf("after full payment: %+v", after.Invoice)
	}
	if res := b.pay(b.owner, inv.ID, "1.00", "pay-0004-dddd"); res.ErrorCode(t) != "invoice_paid" {
		t.Fatalf("paying a paid invoice must fail, got %s", res.Body)
	}

	var ledgerIn decimal.Decimal
	if err := b.env.DB.Owner.QueryRow(context.Background(),
		"SELECT coalesce(sum(amount),0) FROM transactions WHERE organization_id = $1 AND direction = 'in'", b.org).Scan(&ledgerIn); err != nil {
		t.Fatal(err)
	}
	if !ledgerIn.Equal(decimal.RequireFromString("1000")) {
		t.Fatalf("ledger should hold 1000.00 of receipts, got %s", ledgerIn)
	}
}

func TestDuplicatePaymentIsIdempotent(t *testing.T) {
	b := newBooks(t, "USD")
	inv := b.sentInvoice(t, "500.00").Invoice

	first := b.owner.Must(http.StatusCreated, b.pay(b.owner, inv.ID, "200.00", "retry-key-123"))
	replay := b.owner.Must(http.StatusOK, b.pay(b.owner, inv.ID, "200.00", "retry-key-123"))
	if replay.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatal("replayed request must be flagged")
	}
	if first.Map(t)["payment"].(map[string]any)["id"] != replay.Map(t)["payment"].(map[string]any)["id"] {
		t.Fatal("replay must return the original payment")
	}
	conflict := b.pay(b.owner, inv.ID, "250.00", "retry-key-123")
	if conflict.Status != http.StatusConflict || conflict.ErrorCode(t) != "idempotency_conflict" {
		t.Fatalf("reusing a key for a different payment must conflict, got %d %s", conflict.Status, conflict.Body)
	}
	if res := b.owner.Post(b.url("/invoices/"+inv.ID+"/payments"), map[string]string{"amount": "1", "paid_on": "2026-01-01", "method": "cash"}); res.Status != http.StatusBadRequest {
		t.Fatalf("missing idempotency key must be rejected, got %d", res.Status)
	}

	var n int
	_ = b.env.DB.Owner.QueryRow(context.Background(), "SELECT count(*) FROM payments WHERE invoice_id = $1", inv.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("expected exactly one payment, found %d", n)
	}
}

func TestConcurrentPaymentsNeverOverpay(t *testing.T) {
	b := newBooks(t, "USD")
	inv := b.sentInvoice(t, "100.00").Invoice

	const workers = 10
	var wg sync.WaitGroup
	statuses := make([]int, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = b.pay(b.owner, inv.ID, "30.00", fmt.Sprintf("concurrent-%02d", i)).Status
		}(i)
	}
	wg.Wait()

	ok := 0
	for _, s := range statuses {
		switch s {
		case http.StatusCreated:
			ok++
		case http.StatusUnprocessableEntity:
		default:
			t.Fatalf("unexpected status %d", s)
		}
	}
	if ok != 3 {
		t.Fatalf("exactly 3 payments of 30.00 fit into 100.00, got %d", ok)
	}
	var paid decimal.Decimal
	var count int
	if err := b.env.DB.Owner.QueryRow(context.Background(),
		"SELECT i.amount_paid, (SELECT count(*) FROM payments p WHERE p.invoice_id = i.id) FROM invoices i WHERE id = $1", inv.ID).Scan(&paid, &count); err != nil {
		t.Fatal(err)
	}
	if !paid.Equal(decimal.RequireFromString("90")) || count != 3 {
		t.Fatalf("amount_paid=%s payments=%d", paid, count)
	}
}

func TestConcurrentRetriesWithSameKeyCreateOnePayment(t *testing.T) {
	b := newBooks(t, "USD")
	inv := b.sentInvoice(t, "100.00").Invoice
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.pay(b.owner, inv.ID, "40.00", "double-click-key")
		}()
	}
	wg.Wait()
	var n int
	_ = b.env.DB.Owner.QueryRow(context.Background(), "SELECT count(*) FROM payments WHERE invoice_id = $1", inv.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("expected one payment for one idempotency key, got %d", n)
	}
}

func TestPaymentReversalRestoresBalance(t *testing.T) {
	b := newBooks(t, "USD")
	inv := b.sentInvoice(t, "300.00").Invoice
	var res struct {
		Payment struct{ ID string } `json:"payment"`
	}
	b.owner.Must(http.StatusCreated, b.pay(b.owner, inv.ID, "300.00", "full-pay-key")).JSON(t, &res)

	if r := b.owner.Post(b.url("/payments/"+res.Payment.ID+"/reverse"), map[string]string{"reason": ""}); r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("reversal needs a reason, got %d", r.Status)
	}
	b.owner.Must(http.StatusOK, b.owner.Post(b.url("/payments/"+res.Payment.ID+"/reverse"), map[string]string{"reason": "Bounced"}))
	var after invoiceBody
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/invoices/"+inv.ID))).JSON(t, &after)
	if after.Invoice.Status != "sent" || after.Invoice.Balance != "300.00" || after.Invoice.Payments[0].Status != "reversed" {
		t.Fatalf("after reversal: %+v", after.Invoice)
	}
	if r := b.owner.Post(b.url("/payments/"+res.Payment.ID+"/reverse"), map[string]string{"reason": "again"}); r.ErrorCode(t) != "already_reversed" {
		t.Fatalf("double reversal must fail: %s", r.Body)
	}
	var net decimal.Decimal
	_ = b.env.DB.Owner.QueryRow(context.Background(),
		"SELECT coalesce(sum(CASE direction WHEN 'in' THEN amount ELSE -amount END),0) FROM transactions WHERE organization_id = $1", b.org).Scan(&net)
	if !net.IsZero() {
		t.Fatalf("ledger must net to zero after a reversal, got %s", net)
	}
}

func TestOverdueInvoices(t *testing.T) {
	b := newBooks(t, "USD")
	past := b.invoice(t, map[string]any{"issue_date": "2025-01-01", "due_date": "2025-01-31",
		"items": []map[string]string{{"description": "Old work", "quantity": "1", "unit_price": "75"}}})
	b.owner.Must(http.StatusOK, b.owner.Post(b.url("/invoices/"+past.Invoice.ID+"/send"), nil))
	b.sentInvoice(t, "50")
	b.invoice(t, map[string]any{"issue_date": "2025-01-01", "due_date": "2025-01-02", "items": []map[string]string{{"description": "draft", "quantity": "1", "unit_price": "5"}}})

	var got invoiceBody
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/invoices/"+past.Invoice.ID))).JSON(t, &got)
	if !got.Invoice.IsOverdue || got.Invoice.DisplayStatus != "overdue" || got.Invoice.Status != "sent" {
		t.Fatalf("expected overdue: %+v", got.Invoice)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Total int `json:"total"`
	}
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/invoices?status=overdue"))).JSON(t, &list)
	if list.Total != 1 || list.Data[0].ID != past.Invoice.ID {
		t.Fatalf("overdue filter returned %+v", list)
	}
	b.owner.Must(http.StatusCreated, b.pay(b.owner, past.Invoice.ID, "75", "settle-overdue"))
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/invoices/"+past.Invoice.ID))).JSON(t, &got)
	if got.Invoice.IsOverdue {
		t.Fatal("a paid invoice is never overdue")
	}
}

func TestInvoiceLifecycleRules(t *testing.T) {
	b := newBooks(t, "USD")
	inv := b.invoice(t, map[string]any{"items": []map[string]string{{"description": "x", "quantity": "1", "unit_price": "10"}}}).Invoice

	if r := b.pay(b.owner, inv.ID, "5", "draft-payment"); r.ErrorCode(t) != "invoice_not_sent" {
		t.Fatalf("drafts cannot take payments: %s", r.Body)
	}
	stale := b.owner.Patch(b.url("/invoices/"+inv.ID), map[string]any{"customer_id": b.customer, "issue_date": "2026-01-01", "due_date": "2026-01-31",
		"version": inv.Version + 5, "items": []map[string]string{{"description": "y", "quantity": "2", "unit_price": "10"}}})
	if stale.Status != http.StatusConflict {
		t.Fatalf("stale version must conflict, got %d", stale.Status)
	}
	var edited invoiceBody
	b.owner.Must(http.StatusOK, b.owner.Patch(b.url("/invoices/"+inv.ID), map[string]any{"customer_id": b.customer, "issue_date": "2026-01-01",
		"due_date": "2026-01-31", "version": inv.Version, "items": []map[string]string{{"description": "y", "quantity": "2", "unit_price": "10"}}})).JSON(t, &edited)
	if edited.Invoice.Total != "20.00" {
		t.Fatalf("edit should recalculate, got %s", edited.Invoice.Total)
	}
	b.owner.Must(http.StatusOK, b.owner.Post(b.url("/invoices/"+inv.ID+"/send"), nil))
	b.owner.Must(http.StatusCreated, b.pay(b.owner, inv.ID, "5", "lock-it-key"))

	if r := b.owner.Patch(b.url("/invoices/"+inv.ID), map[string]any{"customer_id": b.customer, "issue_date": "2026-01-01", "due_date": "2026-01-31",
		"items": []map[string]string{{"description": "z", "quantity": "1", "unit_price": "1"}}}); r.ErrorCode(t) != "invoice_locked" {
		t.Fatalf("paid invoices are locked: %s", r.Body)
	}
	if r := b.owner.Post(b.url("/invoices/"+inv.ID+"/void"), map[string]string{"reason": "mistake"}); r.ErrorCode(t) != "has_payments" {
		t.Fatalf("cannot void with payments: %s", r.Body)
	}

	other := b.sentInvoice(t, "10").Invoice
	b.owner.Must(http.StatusOK, b.owner.Post(b.url("/invoices/"+other.ID+"/void"), map[string]string{"reason": "Duplicate"}))
	if r := b.owner.Post(b.url("/invoices/"+other.ID+"/send"), nil); r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("void is terminal, got %d", r.Status)
	}
}

func TestDatabaseProtectsFinancialHistory(t *testing.T) {
	b := newBooks(t, "USD")
	inv := b.sentInvoice(t, "100").Invoice
	b.owner.Must(http.StatusCreated, b.pay(b.owner, inv.ID, "40", "history-key"))
	ctx := context.Background()
	org := uuid.MustParse(b.org)

	attempts := map[string]string{
		"change payment amount":   "UPDATE payments SET amount = 1",
		"delete payment":          "DELETE FROM payments",
		"change paid invoice":     "UPDATE invoices SET total = 1, subtotal = 1",
		"delete invoice":          "DELETE FROM invoices",
		"edit ledger":             "UPDATE transactions SET amount = 1",
		"overpay via raw SQL":     "UPDATE invoices SET amount_paid = total + 1",
		"inconsistent status":     "UPDATE invoices SET status = 'paid'",
		"edit locked line items":  "UPDATE invoice_items SET unit_price = 0",
		"negative payment amount": "INSERT INTO payments (organization_id, invoice_id, amount, currency, paid_on, method, idempotency_key, request_fingerprint) SELECT organization_id, id, -5, currency, now(), 'cash', 'negative-key', '\\x00' FROM invoices",
	}
	for name, sql := range attempts {
		err := tenant.WithTx(ctx, b.env.DB.App, tenant.Scope{OrgID: org}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, sql)
			return err
		})
		if err == nil {
			t.Errorf("%s: expected the database to refuse", name)
		}
	}
}

func TestFailedTransactionRollsBackEverything(t *testing.T) {
	b := newBooks(t, "USD")
	inv := b.sentInvoice(t, "100").Invoice
	ctx := context.Background()
	org := uuid.MustParse(b.org)
	boom := errors.New("simulated failure after writing")

	err := tenant.WithTx(ctx, b.env.DB.App, tenant.Scope{OrgID: org}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.InsertPayment(ctx, store.InsertPaymentParams{OrganizationID: org, InvoiceID: uuid.MustParse(inv.ID),
			Amount: decimal.RequireFromString("60"), Currency: "USD", PaidOn: time.Now().UTC().Truncate(24 * time.Hour), Method: "cash",
			IdempotencyKey: "rollback-test", RequestFingerprint: []byte{1}, Source: "internal"}); err != nil {
			return err
		}
		if _, err := q.SetInvoicePaid(ctx, store.SetInvoicePaidParams{ID: uuid.MustParse(inv.ID), AmountPaid: decimal.RequireFromString("60"), Status: "partially_paid"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected simulated failure, got %v", err)
	}
	var got invoiceBody
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/invoices/"+inv.ID))).JSON(t, &got)
	if got.Invoice.AmountPaid != "0.00" || got.Invoice.Status != "sent" || len(got.Invoice.Payments) != 0 {
		t.Fatalf("partial writes must be rolled back: %+v", got.Invoice)
	}

	before := b.invoice(t, map[string]any{"items": []map[string]string{{"description": "x", "quantity": "1", "unit_price": "1"}}}).Invoice.Number
	bad := b.owner.Post(b.url("/invoices"), map[string]any{"customer_id": b.customer, "project_id": uuid.NewString(), "issue_date": "2026-01-01",
		"due_date": "2026-01-02", "items": []map[string]string{{"description": "x", "quantity": "1", "unit_price": "1"}}})
	if bad.Status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown project must fail, got %d", bad.Status)
	}
	after := b.invoice(t, map[string]any{"items": []map[string]string{{"description": "x", "quantity": "1", "unit_price": "1"}}}).Invoice.Number
	if before != "INV-000002" || after != "INV-000003" {
		t.Fatalf("a failed create must not consume an invoice number: %s then %s", before, after)
	}
}

func TestCurrencyPrecision(t *testing.T) {
	b := newBooks(t, "JPY")
	inv := b.invoice(t, map[string]any{"tax_rate": "10", "items": []map[string]string{{"description": "Workshop", "quantity": "3", "unit_price": "333.5"}}}).Invoice
	if inv.Total != "1101" || inv.Subtotal != "1001" {
		t.Fatalf("JPY totals must have no decimals: %+v", inv)
	}
	b.owner.Must(http.StatusOK, b.owner.Post(b.url("/invoices/"+inv.ID+"/send"), nil))
	if r := b.pay(b.owner, inv.ID, "100.5", "yen-fraction"); r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("fractional yen must be rejected, got %d", r.Status)
	}

	usd := newBooks(t, "USD")
	sent := usd.sentInvoice(t, "10.00").Invoice
	if r := usd.pay(usd.owner, sent.ID, "1.001", "usd-mills"); r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("USD amounts with 3 decimals must be rejected, got %d", r.Status)
	}
	if r := usd.pay(usd.owner, sent.ID, "1e1", "usd-exponent"); r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("exponent notation must be rejected, got %d", r.Status)
	}
}

func TestBudgetConsumptionAndExpenseApproval(t *testing.T) {
	b := newBooks(t, "USD")
	approver := b.env.Client()
	approverEmail := approver.Register("Accountant")
	b.owner.AddMember(b.org, approverEmail, "accountant")
	pm := b.env.Client()
	pmEmail := pm.Register("Project Manager")
	b.owner.AddMember(b.org, pmEmail, "project_manager")

	var proj struct {
		Project struct{ ID string } `json:"project"`
	}
	pm.Must(http.StatusCreated, pm.Post(b.url("/projects"), map[string]any{"customer_id": b.customer, "name": "Website rebuild", "code": "WEB-1", "start_date": "2026-01-01"})).JSON(t, &proj)
	var cats struct {
		Data []struct{ ID, Name string } `json:"data"`
	}
	pm.Must(http.StatusOK, pm.Get(b.url("/expense-categories"))).JSON(t, &cats)
	var travel string
	for _, c := range cats.Data {
		if c.Name == "Travel" {
			travel = c.ID
		}
	}
	pm.Must(http.StatusCreated, pm.Post(b.url("/budgets"), map[string]any{"project_id": proj.Project.ID, "amount": "1000.00"}))
	pm.Must(http.StatusCreated, pm.Post(b.url("/budgets"), map[string]any{"project_id": proj.Project.ID, "category_id": travel, "amount": "500.00"}))
	if r := pm.Post(b.url("/budgets"), map[string]any{"project_id": proj.Project.ID, "amount": "1"}); r.Status != http.StatusConflict {
		t.Fatalf("duplicate budget line must conflict, got %d", r.Status)
	}

	submit := func(cat, amount string) string {
		var e struct {
			Expense struct{ ID string } `json:"expense"`
		}
		pm.Must(http.StatusCreated, pm.Post(b.url("/expenses"), map[string]any{"category_id": cat, "project_id": proj.Project.ID,
			"vendor_name": "Delta Air Lines", "expense_date": time.Now().Format("2006-01-02"), "amount": amount})).JSON(t, &e)
		return e.Expense.ID
	}
	e1 := submit(travel, "450.00")
	e2 := submit(cats.Data[0].ID, "820.00")

	if r := pm.Post(b.url("/expenses/"+e1+"/approve"), nil); r.Status != http.StatusForbidden {
		t.Fatalf("project managers cannot approve, got %d", r.Status)
	}
	approver.Must(http.StatusOK, approver.Post(b.url("/expenses/"+e1+"/approve"), nil))
	approver.Must(http.StatusOK, approver.Post(b.url("/expenses/"+e2+"/approve"), nil))
	if r := approver.Post(b.url("/expenses/"+e2+"/approve"), nil); r.ErrorCode(t) != "expense_reviewed" {
		t.Fatalf("double approval must fail: %s", r.Body)
	}
	if r := pm.Patch(b.url("/expenses/"+e1), map[string]any{"category_id": travel, "vendor_name": "x", "expense_date": "2026-01-02", "amount": "1"}); r.ErrorCode(t) != "expense_reviewed" {
		t.Fatalf("approved expenses are locked: %s", r.Body)
	}
	own := func() string {
		var e struct {
			Expense struct{ ID string } `json:"expense"`
		}
		approver.Must(http.StatusCreated, approver.Post(b.url("/expenses"), map[string]any{"category_id": travel, "vendor_name": "Taxi",
			"expense_date": "2026-01-05", "amount": "12.00"})).JSON(t, &e)
		return e.Expense.ID
	}()
	if r := approver.Post(b.url("/expenses/"+own+"/approve"), nil); r.Status != http.StatusForbidden {
		t.Fatalf("approvers cannot approve their own expense, got %d", r.Status)
	}
	if r := approver.Post(b.url("/expenses/"+own+"/reject"), map[string]any{}); r.Status != http.StatusUnprocessableEntity && r.Status != http.StatusForbidden {
		t.Fatalf("rejection needs a note, got %d", r.Status)
	}

	var detail struct {
		Project struct {
			Budget struct {
				Total       string `json:"total"`
				Actual      string `json:"actual"`
				PercentUsed string `json:"percent_used"`
				Level       string `json:"level"`
			} `json:"budget"`
		} `json:"project"`
		BudgetLines []struct {
			CategoryName string `json:"category_name"`
			Consumption  struct {
				Actual, Level string
			} `json:"consumption"`
		} `json:"budget_lines"`
	}
	pm.Must(http.StatusOK, pm.Get(b.url("/projects/"+proj.Project.ID))).JSON(t, &detail)
	pb := detail.Project.Budget
	if pb.Total != "1500.00" || pb.Actual != "1270.00" || pb.PercentUsed != "84.7" || pb.Level != "warning" {
		t.Fatalf("project budget %+v", pb)
	}
	levels := map[string]string{}
	for _, l := range detail.BudgetLines {
		levels[l.CategoryName] = l.Consumption.Actual + "/" + l.Consumption.Level
	}
	if levels["General"] != "820.00/warning" || levels["Travel"] != "450.00/warning" {
		t.Fatalf("budget lines %v", levels)
	}
}

func TestRolePermissionsOnFinancialEndpoints(t *testing.T) {
	b := newBooks(t, "USD")
	viewer := b.env.Client()
	b.owner.AddMember(b.org, viewer.Register("Viewer"), "viewer")
	pm := b.env.Client()
	b.owner.AddMember(b.org, pm.Register("PM"), "project_manager")
	inv := b.sentInvoice(t, "100").Invoice

	viewer.Must(http.StatusOK, viewer.Get(b.url("/invoices")))
	viewer.Must(http.StatusOK, viewer.Get(b.url("/invoices/"+inv.ID)))
	checks := []struct {
		c      *apitest.Client
		method string
		path   string
		body   any
	}{
		{viewer, "POST", "/customers", map[string]string{"name": "x"}},
		{viewer, "POST", "/invoices", map[string]any{}},
		{viewer, "POST", "/expenses", map[string]any{}},
		{pm, "POST", "/invoices/" + inv.ID + "/void", map[string]string{"reason": "x"}},
		{pm, "POST", "/customers", map[string]string{"name": "x"}},
	}
	for _, c := range checks {
		if r := c.c.Do(c.method, b.url(c.path), c.body); r.Status != http.StatusForbidden {
			t.Errorf("%s %s: expected 403, got %d", c.method, c.path, r.Status)
		}
	}
	if r := b.pay(pm, inv.ID, "10", "pm-cannot-pay"); r.Status != http.StatusForbidden {
		t.Errorf("project manager must not record payments, got %d", r.Status)
	}
}

func TestFinancialRecordsAreIsolatedBetweenOrganizations(t *testing.T) {
	b := newBooks(t, "USD")
	inv := b.sentInvoice(t, "100").Invoice
	intruder := b.env.Client()
	intruder.Register("Intruder")
	own := intruder.CreateOrg("Intruder Inc", "USD")

	for _, path := range []string{"/invoices/" + inv.ID, "/customers/" + b.customer} {
		if r := intruder.Get("/api/orgs/" + b.org + path); r.Status != http.StatusNotFound {
			t.Errorf("foreign org path %s: expected 404, got %d", path, r.Status)
		}
		if r := intruder.Get("/api/orgs/" + own + path); r.Status != http.StatusNotFound {
			t.Errorf("own org with foreign id %s: expected 404, got %d", path, r.Status)
		}
	}
	if r := b.pay(intruder, inv.ID, "100", "intruder-pay"); r.Status != http.StatusNotFound {
		t.Errorf("paying a foreign invoice: expected 404, got %d", r.Status)
	}
	r := intruder.Post("/api/orgs/"+own+"/invoices/"+inv.ID+"/payments", map[string]string{"amount": "1", "paid_on": "2026-01-01", "method": "cash"}, "Idempotency-Key", "cross-tenant-key")
	if r.Status != http.StatusNotFound {
		t.Errorf("paying a foreign invoice through own org: expected 404, got %d", r.Status)
	}
	if res := intruder.Post("/api/orgs/"+own+"/invoices", map[string]any{"customer_id": b.customer, "issue_date": "2026-01-01", "due_date": "2026-01-02",
		"items": []map[string]string{{"description": "x", "quantity": "1", "unit_price": "1"}}}); res.Status != http.StatusUnprocessableEntity {
		t.Errorf("invoicing another org's customer must fail validation, got %d", res.Status)
	}
	var list struct {
		Total int `json:"total"`
	}
	intruder.Must(http.StatusOK, intruder.Get("/api/orgs/"+own+"/invoices")).JSON(t, &list)
	if list.Total != 0 {
		t.Errorf("intruder must see no invoices, saw %d", list.Total)
	}
}
