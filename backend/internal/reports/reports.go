// Package reports builds the dashboard and financial reports from SQL aggregates. Every figure is
// summed by PostgreSQL in one currency; the Go code only formats and arranges the results.
package reports

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"finopshq/internal/apperr"
	"finopshq/internal/authz"
	"finopshq/internal/budget"
	"finopshq/internal/httpx"
	"finopshq/internal/input"
	"finopshq/internal/money"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

const maxPeriodDays = 366 * 3

type Period struct {
	From time.Time
	To   time.Time
}

func (p Period) JSON() map[string]string {
	return map[string]string{"from": input.FormatDate(p.From), "to": input.FormatDate(p.To)}
}

// PeriodFrom reads from/to, defaulting to the last twelve calendar months up to today.
func PeriodFrom(r *http.Request, today time.Time) (Period, error) {
	from, to, err := input.DateRange(r)
	if err != nil {
		return Period{}, err
	}
	p := Period{To: today, From: time.Date(today.Year(), today.Month()-11, 1, 0, 0, 0, 0, time.UTC)}
	if to != nil {
		p.To = *to
	}
	if from != nil {
		p.From = *from
	} else if to != nil {
		p.From = time.Date(p.To.Year(), p.To.Month()-11, 1, 0, 0, 0, 0, time.UTC)
	}
	if p.To.Before(p.From) {
		return Period{}, apperr.BadRequest("to must not be before from")
	}
	if p.To.Sub(p.From) > maxPeriodDays*24*time.Hour {
		return Period{}, apperr.BadRequest("the date range can be at most three years")
	}
	return p, nil
}

type Service struct {
	pool *pgxpool.Pool
	// SyncActivity supplies recent synchronization jobs for the dashboard.
	SyncActivity func(ctx context.Context, q *store.Queries, limit int32) (any, error)
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) tx(ctx context.Context, p authz.Principal, fn func(q *store.Queries) error) error {
	if err := p.Require(authz.ReportsView); err != nil {
		return err
	}
	return tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error { return fn(store.New(tx)) })
}

type Totals struct {
	Revenue           string `json:"revenue"`
	Expenses          string `json:"expenses"`
	NetIncome         string `json:"net_income"`
	CashIn            string `json:"cash_in"`
	CashOut           string `json:"cash_out"`
	NetCashFlow       string `json:"net_cash_flow"`
	OutstandingAmount string `json:"outstanding_amount"`
	OutstandingCount  int64  `json:"outstanding_count"`
	OverdueAmount     string `json:"overdue_amount"`
	OverdueCount      int64  `json:"overdue_count"`
}

type Month struct {
	Month     string `json:"month"`
	Revenue   string `json:"revenue"`
	Invoiced  string `json:"invoiced"`
	Expenses  string `json:"expenses"`
	NetIncome string `json:"net_income"`
	CashIn    string `json:"cash_in"`
	CashOut   string `json:"cash_out"`
	NetCash   string `json:"net_cash"`
	Collected string `json:"collected"`
}

type ProjectBudget struct {
	ProjectID   uuid.UUID    `json:"project_id"`
	Name        string       `json:"name"`
	Status      string       `json:"status"`
	Budget      string       `json:"budget"`
	Actual      string       `json:"actual"`
	Pending     string       `json:"pending"`
	PercentUsed string       `json:"percent_used"`
	Level       budget.Level `json:"level"`
}

type Transaction struct {
	ID          uuid.UUID `json:"id"`
	OccurredOn  string    `json:"occurred_on"`
	Direction   string    `json:"direction"`
	Amount      string    `json:"amount"`
	Currency    string    `json:"currency"`
	Description string    `json:"description"`
	SourceType  string    `json:"source_type"`
	SourceID    uuid.UUID `json:"source_id"`
	Account     string    `json:"account"`
}

type Dashboard struct {
	Period               map[string]string `json:"period"`
	Currency             string            `json:"currency"`
	Totals               Totals            `json:"totals"`
	OtherCurrencyRecords int64             `json:"other_currency_records"`
	Monthly              []Month           `json:"monthly"`
	Budgets              []ProjectBudget   `json:"budgets"`
	RecentTransactions   []Transaction     `json:"recent_transactions"`
	RecentSync           any               `json:"recent_sync"`
}

func monthsOf(rows []store.MonthlyFinancialsRow, cur string) []Month {
	out := make([]Month, 0, len(rows))
	for _, r := range rows {
		out = append(out, Month{
			Month: r.Month.Format("2006-01"), Revenue: money.Format(r.Revenue, cur), Invoiced: money.Format(r.Invoiced, cur),
			Expenses: money.Format(r.Expenses, cur), NetIncome: money.Format(r.Revenue.Sub(r.Expenses), cur),
			CashIn: money.Format(r.CashIn, cur), CashOut: money.Format(r.CashOut, cur), NetCash: money.Format(r.CashIn.Sub(r.CashOut), cur),
			Collected: money.Format(r.Collected, cur),
		})
	}
	return out
}

func (s *Service) Dashboard(ctx context.Context, p authz.Principal, period Period) (Dashboard, error) {
	cur := p.Org.BaseCurrency
	d := Dashboard{Period: period.JSON(), Currency: cur}
	err := s.tx(ctx, p, func(q *store.Queries) error {
		t, err := q.DashboardTotals(ctx, store.DashboardTotalsParams{Currency: cur, PeriodStart: period.From, PeriodEnd: period.To, Today: p.Today()})
		if err != nil {
			return err
		}
		d.Totals = Totals{
			Revenue: money.Format(t.Revenue, cur), Expenses: money.Format(t.Expenses, cur), NetIncome: money.Format(t.Revenue.Sub(t.Expenses), cur),
			CashIn: money.Format(t.CashIn, cur), CashOut: money.Format(t.CashOut, cur), NetCashFlow: money.Format(t.CashIn.Sub(t.CashOut), cur),
			OutstandingAmount: money.Format(t.OutstandingAmount, cur), OutstandingCount: t.OutstandingCount,
			OverdueAmount: money.Format(t.OverdueAmount, cur), OverdueCount: t.OverdueCount,
		}
		d.OtherCurrencyRecords = t.OtherCurrencyRecords

		months, err := q.MonthlyFinancials(ctx, store.MonthlyFinancialsParams{PeriodStart: period.From, PeriodEnd: period.To, Currency: cur})
		if err != nil {
			return err
		}
		d.Monthly = monthsOf(months, cur)

		bs, err := q.BudgetedProjectsSummary(ctx, store.BudgetedProjectsSummaryParams{Currency: cur, RowLimit: 6})
		if err != nil {
			return err
		}
		d.Budgets = projectBudgets(bs, cur)

		txs, err := q.ListRecentTransactions(ctx, store.ListRecentTransactionsParams{From: &period.From, To: &period.To, RowLimit: 8})
		if err != nil {
			return err
		}
		d.RecentTransactions = make([]Transaction, 0, len(txs))
		for _, t := range txs {
			d.RecentTransactions = append(d.RecentTransactions, Transaction{ID: t.ID, OccurredOn: input.FormatDate(t.OccurredOn), Direction: t.Direction,
				Amount: money.Format(t.Amount, t.Currency), Currency: t.Currency, Description: t.Description, SourceType: t.SourceType,
				SourceID: t.SourceID, Account: t.AccountName})
		}
		d.RecentSync = []any{}
		if s.SyncActivity != nil && p.Can(authz.IntegrationsView) {
			d.RecentSync, err = s.SyncActivity(ctx, q, 5)
		}
		return err
	})
	return d, err
}

func projectBudgets(rows []store.BudgetedProjectsSummaryRow, cur string) []ProjectBudget {
	out := make([]ProjectBudget, 0, len(rows))
	for _, b := range rows {
		a := budget.Assess(b.BudgetTotal, b.ActualTotal, b.PendingTotal)
		out = append(out, ProjectBudget{ProjectID: b.ID, Name: b.Name, Status: b.Status, Budget: money.Format(b.BudgetTotal, cur),
			Actual: money.Format(b.ActualTotal, cur), Pending: money.Format(b.PendingTotal, cur), PercentUsed: a.PercentUsed.StringFixed(1), Level: a.Level})
	}
	return out
}

// Generic report table, rendered as JSON for the UI and as CSV for export.

type Column struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

type Report struct {
	Key         string              `json:"key"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Currency    string              `json:"currency"`
	Period      map[string]string   `json:"period"`
	Columns     []Column            `json:"columns"`
	Rows        []map[string]string `json:"rows"`
	Totals      map[string]string   `json:"totals,omitempty"`
	Notes       []string            `json:"notes,omitempty"`
}

func col(key, label, kind string) Column { return Column{Key: key, Label: label, Kind: kind} }

var Available = []struct{ Key, Title string }{
	{"profit-and-loss", "Profit and loss"},
	{"revenue", "Revenue by customer"},
	{"expenses", "Expenses by category"},
	{"cash-flow", "Cash flow"},
	{"accounts-receivable", "Accounts receivable aging"},
	{"outstanding-invoices", "Outstanding invoices"},
	{"budget-vs-actual", "Budget vs. actual"},
	{"monthly-summary", "Monthly financial summary"},
}

func (s *Service) Build(ctx context.Context, p authz.Principal, key string, period Period) (Report, error) {
	cur := p.Org.BaseCurrency
	rep := Report{Key: key, Currency: cur, Period: period.JSON(), Rows: []map[string]string{}}
	f := func(d decimal.Decimal) string { return money.Format(d, cur) }
	err := s.tx(ctx, p, func(q *store.Queries) error {
		switch key {
		case "profit-and-loss":
			rep.Title, rep.Description = "Profit and loss", "Accrual basis: revenue by invoice issue date (excluding tax), expenses by approved expense date."
			rep.Columns = []Column{col("line", "Line", "text"), col("amount", "Amount", "money")}
			t, err := q.DashboardTotals(ctx, store.DashboardTotalsParams{Currency: cur, PeriodStart: period.From, PeriodEnd: period.To, Today: p.Today()})
			if err != nil {
				return err
			}
			cats, err := q.ExpensesByCategory(ctx, store.ExpensesByCategoryParams{Currency: cur, PeriodStart: period.From, PeriodEnd: period.To})
			if err != nil {
				return err
			}
			rep.Rows = append(rep.Rows, map[string]string{"line": "Revenue", "amount": f(t.Revenue), "_kind": "total"})
			rep.Rows = append(rep.Rows, map[string]string{"line": "Operating expenses", "amount": "", "_kind": "heading"})
			for _, c := range cats {
				rep.Rows = append(rep.Rows, map[string]string{"line": c.Name, "amount": f(c.Total), "_kind": "line"})
			}
			rep.Rows = append(rep.Rows, map[string]string{"line": "Total expenses", "amount": f(t.Expenses), "_kind": "total"})
			rep.Rows = append(rep.Rows, map[string]string{"line": "Net income", "amount": f(t.Revenue.Sub(t.Expenses)), "_kind": "grand"})
			rep.Totals = map[string]string{"revenue": f(t.Revenue), "expenses": f(t.Expenses), "net_income": f(t.Revenue.Sub(t.Expenses))}
			if t.OtherCurrencyRecords > 0 {
				rep.Notes = append(rep.Notes, fmt.Sprintf("%d records in other currencies are not included.", t.OtherCurrencyRecords))
			}

		case "revenue":
			rep.Title, rep.Description = "Revenue by customer", "Invoices issued in the period, excluding drafts and voided invoices."
			rep.Columns = []Column{col("customer", "Customer", "text"), col("invoices", "Invoices", "number"), col("revenue", "Revenue (ex. tax)", "money"),
				col("tax", "Tax", "money"), col("collected", "Collected", "money"), col("share", "Share", "percent")}
			rows, err := q.RevenueByCustomer(ctx, store.RevenueByCustomerParams{Currency: cur, PeriodStart: period.From, PeriodEnd: period.To})
			if err != nil {
				return err
			}
			var total, tax, collected decimal.Decimal
			var count int64
			for _, r := range rows {
				total, tax, collected, count = total.Add(r.Revenue), tax.Add(r.Tax), collected.Add(r.Collected), count+r.InvoiceCount
			}
			for _, r := range rows {
				rep.Rows = append(rep.Rows, map[string]string{"customer": r.Name, "_id": r.ID.String(), "invoices": strconv.FormatInt(r.InvoiceCount, 10),
					"revenue": f(r.Revenue), "tax": f(r.Tax), "collected": f(r.Collected), "share": money.Percent(r.Revenue, total).StringFixed(1)})
			}
			rep.Totals = map[string]string{"customer": "Total", "invoices": strconv.FormatInt(count, 10), "revenue": f(total), "tax": f(tax), "collected": f(collected), "share": "100.0"}

		case "expenses":
			rep.Title, rep.Description = "Expenses by category", "Approved expenses dated in the period."
			rep.Columns = []Column{col("category", "Category", "text"), col("count", "Expenses", "number"), col("total", "Total", "money"), col("share", "Share", "percent")}
			rows, err := q.ExpensesByCategory(ctx, store.ExpensesByCategoryParams{Currency: cur, PeriodStart: period.From, PeriodEnd: period.To})
			if err != nil {
				return err
			}
			var total decimal.Decimal
			var count int64
			for _, r := range rows {
				total, count = total.Add(r.Total), count+r.ExpenseCount
			}
			for _, r := range rows {
				rep.Rows = append(rep.Rows, map[string]string{"category": r.Name, "_id": r.ID.String(), "count": strconv.FormatInt(r.ExpenseCount, 10),
					"total": f(r.Total), "share": money.Percent(r.Total, total).StringFixed(1)})
			}
			rep.Totals = map[string]string{"category": "Total", "count": strconv.FormatInt(count, 10), "total": f(total), "share": "100.0"}
			vendors, err := q.ExpensesByVendor(ctx, store.ExpensesByVendorParams{Currency: cur, PeriodStart: period.From, PeriodEnd: period.To})
			if err != nil {
				return err
			}
			if len(vendors) > 0 {
				top := vendors[0]
				rep.Notes = append(rep.Notes, fmt.Sprintf("Largest vendor: %s (%s across %d expenses).", top.VendorName, f(top.Total), top.ExpenseCount))
			}

		case "cash-flow":
			rep.Title, rep.Description = "Cash flow", "Money received and paid out per month, from the cash ledger."
			rep.Columns = []Column{col("month", "Month", "month"), col("cash_in", "Cash in", "money"), col("cash_out", "Cash out", "money"),
				col("net", "Net cash flow", "money"), col("closing", "Closing balance", "money")}
			opening, err := q.CashBalanceBefore(ctx, store.CashBalanceBeforeParams{Currency: cur, BeforeDate: period.From})
			if err != nil {
				return err
			}
			months, err := q.MonthlyFinancials(ctx, store.MonthlyFinancialsParams{PeriodStart: period.From, PeriodEnd: period.To, Currency: cur})
			if err != nil {
				return err
			}
			bal := opening
			var in, out decimal.Decimal
			for _, m := range months {
				net := m.CashIn.Sub(m.CashOut)
				bal = bal.Add(net)
				in, out = in.Add(m.CashIn), out.Add(m.CashOut)
				rep.Rows = append(rep.Rows, map[string]string{"month": m.Month.Format("2006-01"), "cash_in": f(m.CashIn), "cash_out": f(m.CashOut), "net": f(net), "closing": f(bal)})
			}
			rep.Totals = map[string]string{"month": "Total", "cash_in": f(in), "cash_out": f(out), "net": f(in.Sub(out)), "closing": f(bal), "opening": f(opening)}
			rep.Notes = append(rep.Notes, "Opening balance on "+input.FormatDate(period.From)+": "+f(opening)+". The ledger records invoice payments, payment reversals and approved expenses.")

		case "accounts-receivable":
			rep.Title, rep.Description = "Accounts receivable aging", "Open balances by how long they are past due, as of today."
			rep.Period = map[string]string{"as_of": input.FormatDate(p.Today())}
			rep.Columns = []Column{col("customer", "Customer", "text"), col("current", "Current", "money"), col("d1_30", "1–30 days", "money"),
				col("d31_60", "31–60 days", "money"), col("d61_90", "61–90 days", "money"), col("d90", "Over 90 days", "money"), col("total", "Total", "money")}
			rows, err := q.AgingByCustomer(ctx, store.AgingByCustomerParams{AsOf: p.Today(), Currency: cur})
			if err != nil {
				return err
			}
			var t [6]decimal.Decimal
			for _, r := range rows {
				vals := [6]decimal.Decimal{r.CurrentAmount, r.Days130, r.Days3160, r.Days6190, r.DaysOver90, r.Total}
				for i := range t {
					t[i] = t[i].Add(vals[i])
				}
				rep.Rows = append(rep.Rows, map[string]string{"customer": r.Name, "_id": r.ID.String(), "current": f(vals[0]), "d1_30": f(vals[1]),
					"d31_60": f(vals[2]), "d61_90": f(vals[3]), "d90": f(vals[4]), "total": f(vals[5])})
			}
			rep.Totals = map[string]string{"customer": "Total", "current": f(t[0]), "d1_30": f(t[1]), "d31_60": f(t[2]), "d61_90": f(t[3]), "d90": f(t[4]), "total": f(t[5])}

		case "outstanding-invoices":
			rep.Title, rep.Description = "Outstanding invoices", "Every sent or partially paid invoice with money still owed."
			rep.Period = map[string]string{"as_of": input.FormatDate(p.Today())}
			rep.Columns = []Column{col("number", "Invoice", "text"), col("customer", "Customer", "text"), col("issue_date", "Issued", "date"),
				col("due_date", "Due", "date"), col("days_overdue", "Days overdue", "number"), col("total", "Total", "money"),
				col("paid", "Paid", "money"), col("balance", "Balance", "money")}
			rows, err := q.OpenInvoices(ctx, store.OpenInvoicesParams{AsOf: p.Today(), Currency: cur})
			if err != nil {
				return err
			}
			var total, paid, bal decimal.Decimal
			for _, r := range rows {
				total, paid, bal = total.Add(r.Total), paid.Add(r.AmountPaid), bal.Add(r.Balance)
				rep.Rows = append(rep.Rows, map[string]string{"number": r.Number, "_id": r.ID.String(), "customer": r.CustomerName,
					"issue_date": input.FormatDate(r.IssueDate), "due_date": input.FormatDate(r.DueDate), "days_overdue": strconv.Itoa(int(r.DaysOverdue)),
					"total": f(r.Total), "paid": f(r.AmountPaid), "balance": f(r.Balance)})
			}
			rep.Totals = map[string]string{"number": fmt.Sprintf("%d invoices", len(rows)), "total": f(total), "paid": f(paid), "balance": f(bal)}

		case "budget-vs-actual":
			rep.Title, rep.Description = "Budget vs. actual", "Every budgeted project, comparing approved spend with its budget."
			rep.Columns = []Column{col("project", "Project", "text"), col("status", "Status", "text"), col("budget", "Budget", "money"),
				col("actual", "Actual", "money"), col("pending", "Pending", "money"), col("remaining", "Remaining", "money"),
				col("used", "Used", "percent"), col("level", "Health", "text")}
			rows, err := q.BudgetedProjectsSummary(ctx, store.BudgetedProjectsSummaryParams{Currency: cur, RowLimit: 1000})
			if err != nil {
				return err
			}
			var b, a, pe decimal.Decimal
			for _, r := range rows {
				as := budget.Assess(r.BudgetTotal, r.ActualTotal, r.PendingTotal)
				b, a, pe = b.Add(r.BudgetTotal), a.Add(r.ActualTotal), pe.Add(r.PendingTotal)
				rep.Rows = append(rep.Rows, map[string]string{"project": r.Name, "_id": r.ID.String(), "status": r.Status, "budget": f(r.BudgetTotal),
					"actual": f(r.ActualTotal), "pending": f(r.PendingTotal), "remaining": f(as.Remaining), "used": as.PercentUsed.StringFixed(1), "level": string(as.Level)})
			}
			total := budget.Assess(b, a, pe)
			rep.Totals = map[string]string{"project": "Total", "budget": f(b), "actual": f(a), "pending": f(pe), "remaining": f(total.Remaining), "used": total.PercentUsed.StringFixed(1)}

		case "monthly-summary":
			rep.Title, rep.Description = "Monthly financial summary", "Invoicing, revenue, collections and spending per month."
			rep.Columns = []Column{col("month", "Month", "month"), col("invoiced", "Invoiced (inc. tax)", "money"), col("revenue", "Revenue", "money"),
				col("collected", "Collected", "money"), col("expenses", "Expenses", "money"), col("net_income", "Net income", "money"), col("net_cash", "Net cash", "money")}
			months, err := q.MonthlyFinancials(ctx, store.MonthlyFinancialsParams{PeriodStart: period.From, PeriodEnd: period.To, Currency: cur})
			if err != nil {
				return err
			}
			var inv, rev, col_, exp, cash decimal.Decimal
			for _, m := range monthsOf(months, cur) {
				rep.Rows = append(rep.Rows, map[string]string{"month": m.Month, "invoiced": m.Invoiced, "revenue": m.Revenue, "collected": m.Collected,
					"expenses": m.Expenses, "net_income": m.NetIncome, "net_cash": m.NetCash})
			}
			for _, m := range months {
				inv, rev, col_, exp, cash = inv.Add(m.Invoiced), rev.Add(m.Revenue), col_.Add(m.Collected), exp.Add(m.Expenses), cash.Add(m.CashIn.Sub(m.CashOut))
			}
			rep.Totals = map[string]string{"month": "Total", "invoiced": f(inv), "revenue": f(rev), "collected": f(col_), "expenses": f(exp),
				"net_income": f(rev.Sub(exp)), "net_cash": f(cash)}

		default:
			return apperr.NotFound("report")
		}
		return nil
	})
	return rep, err
}

// WriteCSV writes the report as CSV. Text cells that a spreadsheet would treat as formulas are
// prefixed with an apostrophe.
func WriteCSV(w http.ResponseWriter, rep Report) error {
	name := rep.Key
	if from, ok := rep.Period["from"]; ok {
		name += "_" + from + "_" + rep.Period["to"]
	} else if asOf, ok := rep.Period["as_of"]; ok {
		name += "_" + asOf
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, name))
	cw := csv.NewWriter(w)
	header := make([]string, len(rep.Columns))
	for i, c := range rep.Columns {
		header[i] = c.Label
		if c.Kind == "money" {
			header[i] += " (" + rep.Currency + ")"
		}
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	write := func(row map[string]string) error {
		rec := make([]string, len(rep.Columns))
		for i, c := range rep.Columns {
			v := row[c.Key]
			if c.Kind == "text" {
				v = SafeCell(v)
			}
			rec[i] = v
		}
		return cw.Write(rec)
	}
	for _, r := range rep.Rows {
		if err := write(r); err != nil {
			return err
		}
	}
	if rep.Totals != nil {
		if err := write(rep.Totals); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func SafeCell(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

type Handler struct{ svc *Service }

func NewHandler(s *Service) *Handler { return &Handler{svc: s} }

func (h *Handler) DashboardRoute(w http.ResponseWriter, r *http.Request) {
	p := authz.MustPrincipal(r.Context())
	period, err := PeriodFrom(r, p.Today())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	d, err := h.svc.Dashboard(r.Context(), p, period)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) Routes(rt chi.Router) {
	rt.Get("/", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": Available})
	})
	rt.Get("/{key}", func(w http.ResponseWriter, r *http.Request) {
		p := authz.MustPrincipal(r.Context())
		period, err := PeriodFrom(r, p.Today())
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		rep, err := h.svc.Build(r.Context(), p, chi.URLParam(r, "key"), period)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		if r.URL.Query().Get("format") == "csv" {
			if err := WriteCSV(w, rep); err != nil {
				httpx.WriteError(w, r, err)
			}
			return
		}
		httpx.WriteJSON(w, http.StatusOK, rep)
	})
}
