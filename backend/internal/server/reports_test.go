package server_test

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"
	"time"

	"finopshq/internal/reports"
)

func day(offset int) string { return time.Now().AddDate(0, 0, offset).Format("2006-01-02") }

// seedLedger creates a small, fully known set of books:
//   - INV A: 1000.00 + 10% tax, issued 40 days ago, due 10 days ago, 300.00 paid  -> overdue 800.00
//   - INV B: 500.00, issued 5 days ago, due in 25 days, unpaid                   -> outstanding 500.00
//   - INV C: draft 999.00 (never counts)
//   - expense 200.00 approved (Travel), 75.50 approved (Software), 60.00 pending
func seedLedger(t *testing.T, b *books) {
	t.Helper()
	a := b.invoice(t, map[string]any{"issue_date": day(-40), "due_date": day(-10), "tax_rate": "10",
		"items": []map[string]string{{"description": "Audit", "quantity": "1", "unit_price": "1000"}}}).Invoice
	b.owner.Must(http.StatusOK, b.owner.Post(b.url("/invoices/"+a.ID+"/send"), nil))
	b.owner.Must(http.StatusCreated, b.pay(b.owner, a.ID, "300.00", "report-pay-1"))

	inv := b.invoice(t, map[string]any{"issue_date": day(-5), "due_date": day(25), "items": []map[string]string{{"description": "Retainer", "quantity": "1", "unit_price": "500"}}}).Invoice
	b.owner.Must(http.StatusOK, b.owner.Post(b.url("/invoices/"+inv.ID+"/send"), nil))
	b.invoice(t, map[string]any{"items": []map[string]string{{"description": "Draft", "quantity": "1", "unit_price": "999"}}})

	var cats struct {
		Data []struct{ ID, Name string } `json:"data"`
	}
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/expense-categories"))).JSON(t, &cats)
	cat := map[string]string{}
	for _, c := range cats.Data {
		cat[c.Name] = c.ID
	}
	approver := b.env.Client()
	b.owner.AddMember(b.org, approver.Register("Approver"), "accountant")
	add := func(category, vendor, amount string, approve bool) {
		var e struct {
			Expense struct{ ID string } `json:"expense"`
		}
		b.owner.Must(http.StatusCreated, b.owner.Post(b.url("/expenses"), map[string]any{"category_id": cat[category], "vendor_name": vendor,
			"expense_date": day(-3), "amount": amount})).JSON(t, &e)
		if approve {
			approver.Must(http.StatusOK, approver.Post(b.url("/expenses/"+e.Expense.ID+"/approve"), nil))
		}
	}
	add("Travel", "United Airlines", "200.00", true)
	add("Software & Subscriptions", "=HYPERLINK(\"http://evil\")", "75.50", true)
	add("Travel", "Hilton", "60.00", false)
}

func TestDashboardMetricsComeFromTheBooks(t *testing.T) {
	b := newBooks(t, "USD")
	seedLedger(t, b)

	var d reports.Dashboard
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/dashboard?from="+day(-60)+"&to="+day(0)))).JSON(t, &d)
	tot := d.Totals
	want := map[string]string{
		"revenue": "1500.00", "expenses": "275.50", "net_income": "1224.50", "cash_in": "300.00", "cash_out": "275.50",
		"net_cash_flow": "24.50", "outstanding": "1300.00", "overdue": "800.00",
	}
	got := map[string]string{
		"revenue": tot.Revenue, "expenses": tot.Expenses, "net_income": tot.NetIncome, "cash_in": tot.CashIn, "cash_out": tot.CashOut,
		"net_cash_flow": tot.NetCashFlow, "outstanding": tot.OutstandingAmount, "overdue": tot.OverdueAmount,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %s, want %s", k, got[k], v)
		}
	}
	if tot.OutstandingCount != 2 || tot.OverdueCount != 1 {
		t.Errorf("counts: outstanding %d overdue %d", tot.OutstandingCount, tot.OverdueCount)
	}
	if len(d.Monthly) < 2 || len(d.RecentTransactions) != 3 {
		t.Errorf("monthly %d, transactions %d", len(d.Monthly), len(d.RecentTransactions))
	}

	var narrow reports.Dashboard
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/dashboard?from="+day(-10)+"&to="+day(0)))).JSON(t, &narrow)
	if narrow.Totals.Revenue != "500.00" {
		t.Errorf("date filter should exclude the older invoice, revenue = %s", narrow.Totals.Revenue)
	}
	if narrow.Totals.OutstandingAmount != "1300.00" {
		t.Errorf("receivables are a balance as of today, not filtered by period: %s", narrow.Totals.OutstandingAmount)
	}

	if r := b.owner.Get(b.url("/dashboard?from=2026-05-01&to=2026-01-01")); r.Status != http.StatusBadRequest {
		t.Errorf("inverted range should be rejected, got %d", r.Status)
	}
	if r := b.owner.Get(b.url("/dashboard?from=2010-01-01&to=2026-01-01")); r.Status != http.StatusBadRequest {
		t.Errorf("ranges over three years should be rejected, got %d", r.Status)
	}
}

func TestDashboardIsPerOrganization(t *testing.T) {
	b := newBooks(t, "USD")
	seedLedger(t, b)
	other := b.env.Client()
	other.Register("Other")
	org := other.CreateOrg("Empty Co", "USD")
	var d reports.Dashboard
	other.Must(http.StatusOK, other.Get("/api/orgs/"+org+"/dashboard")).JSON(t, &d)
	if d.Totals.Revenue != "0.00" || d.Totals.OutstandingAmount != "0.00" || len(d.RecentTransactions) != 0 {
		t.Fatalf("another organization must see none of these figures: %+v", d.Totals)
	}
}

func TestOtherCurrenciesAreExcludedAndCounted(t *testing.T) {
	b := newBooks(t, "USD")
	var eur struct {
		Customer struct{ ID string } `json:"customer"`
	}
	b.owner.Must(http.StatusCreated, b.owner.Post(b.url("/customers"), map[string]any{"name": "Euro GmbH", "currency": "EUR"})).JSON(t, &eur)
	inv := b.invoice(t, map[string]any{"customer_id": eur.Customer.ID, "items": []map[string]string{{"description": "x", "quantity": "1", "unit_price": "100"}}}).Invoice
	b.owner.Must(http.StatusOK, b.owner.Post(b.url("/invoices/"+inv.ID+"/send"), nil))

	var d reports.Dashboard
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/dashboard"))).JSON(t, &d)
	if d.Totals.Revenue != "0.00" || d.OtherCurrencyRecords != 1 {
		t.Fatalf("EUR revenue must not be added to USD totals: revenue %s, other %d", d.Totals.Revenue, d.OtherCurrencyRecords)
	}
}

func TestReports(t *testing.T) {
	b := newBooks(t, "USD")
	seedLedger(t, b)
	q := "?from=" + day(-60) + "&to=" + day(0)

	var pnl reports.Report
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/reports/profit-and-loss"+q))).JSON(t, &pnl)
	if pnl.Totals["net_income"] != "1224.50" || len(pnl.Rows) != 6 {
		t.Fatalf("p&l totals %v rows %d", pnl.Totals, len(pnl.Rows))
	}

	var aging reports.Report
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/reports/accounts-receivable"))).JSON(t, &aging)
	row := aging.Rows[0]
	if row["d1_30"] != "800.00" || row["current"] != "500.00" || row["total"] != "1300.00" {
		t.Fatalf("aging buckets %v", row)
	}

	var open reports.Report
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/reports/outstanding-invoices"))).JSON(t, &open)
	if len(open.Rows) != 2 || open.Rows[0]["days_overdue"] != "10" || open.Totals["balance"] != "1300.00" {
		t.Fatalf("outstanding report %v / %v", open.Rows, open.Totals)
	}

	var cash reports.Report
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/reports/cash-flow"+q))).JSON(t, &cash)
	if cash.Totals["net"] != "24.50" || cash.Totals["closing"] != "24.50" || cash.Totals["opening"] != "0.00" {
		t.Fatalf("cash flow %v", cash.Totals)
	}

	var exp reports.Report
	b.owner.Must(http.StatusOK, b.owner.Get(b.url("/reports/expenses"+q))).JSON(t, &exp)
	if exp.Totals["total"] != "275.50" || exp.Rows[0]["category"] != "Travel" || exp.Rows[0]["share"] != "72.6" {
		t.Fatalf("expense report %v %v", exp.Rows, exp.Totals)
	}

	for _, key := range []string{"revenue", "budget-vs-actual", "monthly-summary"} {
		b.owner.Must(http.StatusOK, b.owner.Get(b.url("/reports/"+key+q)))
	}
	b.owner.Must(http.StatusNotFound, b.owner.Get(b.url("/reports/nope")))
}

func TestReportCSVExport(t *testing.T) {
	b := newBooks(t, "USD")
	seedLedger(t, b)
	res := b.owner.Must(http.StatusOK, b.owner.Get(b.url("/reports/expenses?format=csv&from="+day(-60)+"&to="+day(0))))
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/csv") || !strings.Contains(res.Header.Get("Content-Disposition"), "expenses_") {
		t.Fatalf("headers %v", res.Header)
	}
	rows, err := csv.NewReader(strings.NewReader(string(res.Body))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if rows[0][2] != "Total (USD)" || rows[1][0] != "Travel" || rows[1][2] != "200.00" || rows[len(rows)-1][2] != "275.50" {
		t.Fatalf("csv %v", rows)
	}

	vendors := b.owner.Must(http.StatusOK, b.owner.Get(b.url("/reports/outstanding-invoices?format=csv")))
	if !strings.Contains(string(vendors.Body), "Globex Corporation") {
		t.Fatal("outstanding csv should list the customer")
	}
	if got := reports.SafeCell(`=HYPERLINK("http://evil")`); !strings.HasPrefix(got, "'=") {
		t.Fatalf("formula cells must be neutralised, got %q", got)
	}
	if got := reports.SafeCell("Acme"); got != "Acme" {
		t.Fatalf("plain text must be untouched, got %q", got)
	}
}
