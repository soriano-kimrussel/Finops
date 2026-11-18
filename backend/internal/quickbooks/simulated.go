package quickbooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// SIMULATED PROVIDER. It does not contact Intuit. It produces QuickBooks-shaped records derived
// from the organization's own data, with deliberate, deterministic discrepancies so that the
// import and reconciliation workflows can be exercised without sandbox credentials. Every record
// it returns carries "simulated": true in its raw payload, and connections using it are stored
// with mode = 'simulated' and labelled as such in the UI.

type SnapCustomer struct {
	ID       uuid.UUID
	Name     string
	Email    *string
	Currency string
}

type SnapInvoice struct {
	ID           uuid.UUID
	Number       string
	CustomerID   uuid.UUID
	CustomerName string
	IssueDate    time.Time
	DueDate      time.Time
	Total        decimal.Decimal
	AmountPaid   decimal.Decimal
	Currency     string
}

type SnapPayment struct {
	ID            uuid.UUID
	InvoiceID     uuid.UUID
	InvoiceNumber string
	Amount        decimal.Decimal
	PaidOn        time.Time
	Currency      string
}

type Snapshot struct {
	Customers []SnapCustomer
	Invoices  []SnapInvoice
	Payments  []SnapPayment
	Today     time.Time
}

type Simulated struct {
	Snapshot func(ctx context.Context, orgID uuid.UUID) (Snapshot, error)
	// FailNext, when set, is consulted before each page; tests use it to inject failures.
	FailNext func(entity string, start int) error
}

func (s *Simulated) Mode() string { return ModeSimulated }

func simID(prefix string, id uuid.UUID) string {
	sum := sha256.Sum256(id[:])
	return prefix + hex.EncodeToString(sum[:5])
}

var (
	simExtraCustomerID = "SIM-C-WESTFIELD"
	twentyFive         = decimal.NewFromInt(25)
)

func rawOf(v map[string]any) json.RawMessage {
	v["simulated"] = true
	b, _ := json.Marshal(v)
	return b
}

func page[T any](all []T, start, max int) []T {
	if start < 1 {
		start = 1
	}
	if start > len(all) {
		return nil
	}
	end := start - 1 + max
	if end > len(all) {
		end = len(all)
	}
	return all[start-1 : end]
}

func (s *Simulated) check(entity string, start int) error {
	if s.FailNext != nil {
		return s.FailNext(entity, start)
	}
	return nil
}

func (s *Simulated) Customers(ctx context.Context, sess Session, start, max int) ([]Customer, error) {
	if err := s.check("Customer", start); err != nil {
		return nil, err
	}
	snap, err := s.Snapshot(ctx, sess.OrgID)
	if err != nil {
		return nil, err
	}
	var all []Customer
	for _, c := range snap.Customers {
		email := ""
		if c.Email != nil {
			email = *c.Email
		}
		id := simID("SIM-C-", c.ID)
		all = append(all, Customer{ID: id, DisplayName: c.Name, Email: email, Currency: c.Currency, Active: true,
			Raw: rawOf(map[string]any{"Id": id, "DisplayName": c.Name, "PrimaryEmailAddr": map[string]string{"Address": email}, "CurrencyRef": map[string]string{"value": c.Currency}})})
	}
	all = append(all, Customer{ID: simExtraCustomerID, DisplayName: "Westfield Legal Partners", Email: "accounts@westfieldlegal.example",
		Currency: sess.BaseCurrency, Active: true, City: "St. Louis", Region: "MO", Country: "United States",
		Raw: rawOf(map[string]any{"Id": simExtraCustomerID, "DisplayName": "Westfield Legal Partners", "CurrencyRef": map[string]string{"value": sess.BaseCurrency}})})
	return page(all, start, max), nil
}

func (s *Simulated) invoices(snap Snapshot, base string) []Invoice {
	var all []Invoice
	for i, inv := range snap.Invoices {
		if i%11 == 10 {
			continue // missing in QuickBooks
		}
		total, txn := inv.Total, inv.IssueDate
		if i%9 == 4 {
			total = total.Add(twentyFive)
		}
		if i%7 == 3 {
			txn = txn.AddDate(0, 0, 2)
		}
		due := inv.DueDate
		balance := total.Sub(inv.AmountPaid)
		if balance.IsNegative() {
			balance = decimal.Zero
		}
		id := simID("SIM-I-", inv.ID)
		custID := simID("SIM-C-", inv.CustomerID)
		all = append(all, Invoice{ID: id, DocNumber: inv.Number, CustomerID: custID, CustomerName: inv.CustomerName, TxnDate: txn, DueDate: &due,
			Total: total, Balance: balance, Currency: inv.Currency,
			Lines: []InvoiceLine{{Description: "Services per " + inv.Number, Amount: total}},
			Raw: rawOf(map[string]any{"Id": id, "DocNumber": inv.Number, "TxnDate": txn.Format("2006-01-02"), "DueDate": due.Format("2006-01-02"),
				"TotalAmt": total, "Balance": balance, "CustomerRef": map[string]string{"value": custID, "name": inv.CustomerName}})})
	}
	for _, x := range []struct {
		id, doc string
		amount  string
		ago     int
	}{{"SIM-I-X1043", "QB-1043", "1850.00", 12}, {"SIM-I-X1044", "QB-1044", "640.00", 3}} {
		amt := decimal.RequireFromString(x.amount)
		txn := snap.Today.AddDate(0, 0, -x.ago)
		due := txn.AddDate(0, 0, 30)
		all = append(all, Invoice{ID: x.id, DocNumber: x.doc, CustomerID: simExtraCustomerID, CustomerName: "Westfield Legal Partners",
			TxnDate: txn, DueDate: &due, Total: amt, Balance: amt, Currency: base,
			Lines: []InvoiceLine{{Description: "Contract review", Amount: amt}},
			Raw:   rawOf(map[string]any{"Id": x.id, "DocNumber": x.doc, "TxnDate": txn.Format("2006-01-02"), "TotalAmt": amt})})
	}
	return all
}

func (s *Simulated) Invoices(ctx context.Context, sess Session, start, max int) ([]Invoice, error) {
	if err := s.check("Invoice", start); err != nil {
		return nil, err
	}
	snap, err := s.Snapshot(ctx, sess.OrgID)
	if err != nil {
		return nil, err
	}
	return page(s.invoices(snap, sess.BaseCurrency), start, max), nil
}

func (s *Simulated) Payments(ctx context.Context, sess Session, start, max int) ([]Payment, error) {
	if err := s.check("Payment", start); err != nil {
		return nil, err
	}
	snap, err := s.Snapshot(ctx, sess.OrgID)
	if err != nil {
		return nil, err
	}
	present := map[uuid.UUID]bool{}
	for i, inv := range snap.Invoices {
		if i%11 != 10 {
			present[inv.ID] = true
		}
	}
	var all []Payment
	for i, p := range snap.Payments {
		if !present[p.InvoiceID] {
			continue
		}
		amount := p.Amount
		if i%13 == 6 && amount.GreaterThan(decimal.NewFromInt(10)) {
			amount = amount.Sub(decimal.NewFromInt(10))
		}
		id := simID("SIM-P-", p.ID)
		invID := simID("SIM-I-", p.InvoiceID)
		all = append(all, Payment{ID: id, TxnDate: p.PaidOn, Total: amount, Currency: p.Currency,
			Applied: []PaymentApplication{{InvoiceID: invID, Amount: amount}},
			Raw: rawOf(map[string]any{"Id": id, "TxnDate": p.PaidOn.Format("2006-01-02"), "TotalAmt": amount,
				"Line": []any{map[string]any{"Amount": amount, "LinkedTxn": []any{map[string]string{"TxnId": invID, "TxnType": "Invoice"}}}}})})
	}
	part := decimal.RequireFromString("500.00")
	txn := snap.Today.AddDate(0, 0, -2)
	all = append(all, Payment{ID: "SIM-P-X1043", TxnDate: txn, Total: part, Currency: sess.BaseCurrency,
		Applied: []PaymentApplication{{InvoiceID: "SIM-I-X1043", Amount: part}},
		Raw:     rawOf(map[string]any{"Id": "SIM-P-X1043", "TxnDate": txn.Format("2006-01-02"), "TotalAmt": part})})
	return page(all, start, max), nil
}
