package invoices

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"finopshq/internal/apperr"
	"finopshq/internal/audit"
	"finopshq/internal/authz"
	"finopshq/internal/input"
	"finopshq/internal/money"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Preview computes totals for an unsaved invoice. The browser shows these numbers instead of
// doing its own arithmetic.
func (s *Service) Preview(p authz.Principal, in Input) (PreviewResult, error) {
	if err := p.Require(authz.InvoicesView); err != nil {
		return PreviewResult{}, err
	}
	cur := strings.ToUpper(strings.TrimSpace(in.Currency))
	if cur == "" {
		cur = p.Org.BaseCurrency
	}
	var v apperr.Validation
	in.IssueDate, in.DueDate = orDefault(in.IssueDate), orDefault(in.DueDate)
	d := parseInput(in, cur, &v)
	if err := v.Err(); err != nil {
		return PreviewResult{}, err
	}
	return previewOf(d.Totals, cur), nil
}

func orDefault(s string) string {
	if strings.TrimSpace(s) == "" {
		return "2000-01-01"
	}
	return s
}

// resolve validates input against the database: customer, project and currency consistency.
func resolve(ctx context.Context, q *store.Queries, in Input, existingCurrency string) (draft, store.Customer, error) {
	var v apperr.Validation
	custID := input.RequiredUUID(&v, "customer_id", in.CustomerID)
	var cust store.Customer
	if !v.Has("customer_id") {
		c, err := q.GetCustomer(ctx, custID)
		if apperr.IsNoRows(err) {
			v.Add("customer_id", "does not exist")
		} else if err != nil {
			return draft{}, cust, err
		} else {
			cust = c
		}
	}
	cur := strings.ToUpper(strings.TrimSpace(in.Currency))
	if cur == "" {
		cur = existingCurrency
	}
	if cur == "" {
		cur = cust.Currency
	}
	if cur == "" {
		cur = "USD"
	}
	d := parseInput(in, cur, &v)
	d.CustomerID = custID
	if d.ProjectID != nil {
		pr, err := q.GetProject(ctx, *d.ProjectID)
		if apperr.IsNoRows(err) {
			v.Add("project_id", "does not exist")
		} else if err != nil {
			return draft{}, cust, err
		} else {
			if pr.CustomerID != custID {
				v.Add("project_id", "belongs to a different customer")
			}
			if pr.Currency != cur {
				v.Add("currency", fmt.Sprintf("must match the project currency (%s)", pr.Currency))
			}
		}
	}
	return d, cust, v.Err()
}

func writeItems(ctx context.Context, q *store.Queries, orgID, invoiceID uuid.UUID, d draft) error {
	for i, l := range d.Lines {
		if err := q.InsertInvoiceItem(ctx, store.InsertInvoiceItemParams{
			OrganizationID: orgID, InvoiceID: invoiceID, Position: int32(i + 1), Description: l.Description,
			Quantity: l.Quantity, UnitPrice: l.UnitPrice, LineTotal: d.Totals.LineTotals[i],
		}); err != nil {
			return err
		}
	}
	return nil
}

func snapshot(inv Invoice) map[string]any {
	items := make([]map[string]string, 0, len(inv.Items))
	for _, it := range inv.Items {
		items = append(items, map[string]string{"description": it.Description, "quantity": it.Quantity, "unit_price": it.UnitPrice, "line_total": it.LineTotal})
	}
	return map[string]any{
		"number": inv.Number, "status": inv.Status, "customer_id": inv.CustomerID, "project_id": inv.ProjectID,
		"currency": inv.Currency, "issue_date": inv.IssueDate, "due_date": inv.DueDate, "discount_type": inv.DiscountType,
		"discount_value": inv.DiscountValue, "tax_rate": inv.TaxRate, "subtotal": inv.Subtotal, "discount_total": inv.DiscountTotal,
		"tax_total": inv.TaxTotal, "total": inv.Total, "amount_paid": inv.AmountPaid, "items": items,
	}
}

func (s *Service) Create(ctx context.Context, p authz.Principal, in Input) (Invoice, error) {
	if err := p.Require(authz.InvoicesManage); err != nil {
		return Invoice{}, err
	}
	var out Invoice
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		d, cust, err := resolve(ctx, q, in, "")
		if err != nil {
			return err
		}
		if cust.Status != "active" {
			return (&apperr.Validation{}).With("customer_id", "is inactive")
		}
		seq, err := q.NextInvoiceNumber(ctx, p.Org.ID)
		if err != nil {
			return fmt.Errorf("allocate invoice number: %w", err)
		}
		row, err := q.CreateInvoice(ctx, store.CreateInvoiceParams{
			OrganizationID: p.Org.ID, CustomerID: d.CustomerID, ProjectID: d.ProjectID, Number: fmt.Sprintf("%s%06d", seq.Prefix, seq.Value),
			Status: "draft", Currency: d.Currency, IssueDate: d.IssueDate, DueDate: d.DueDate, DiscountType: string(d.DiscountType),
			DiscountValue: d.DiscountValue, TaxRate: d.TaxRate, Subtotal: d.Totals.Subtotal, DiscountTotal: d.Totals.DiscountTotal,
			TaxTotal: d.Totals.TaxTotal, Total: d.Totals.Total, Notes: d.Notes, Source: "internal", CreatedBy: &p.User.ID,
		})
		if err != nil {
			return err
		}
		if err := writeItems(ctx, q, p.Org.ID, row.ID, d); err != nil {
			return err
		}
		out, err = load(ctx, q, row.ID, p)
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "invoice.created", EntityType: "invoice", EntityID: row.ID, After: snapshot(out)})
	})
	return out, err
}

func editable(inv store.Invoice) error {
	switch {
	case inv.Status == "void":
		return apperr.Unprocessable("invoice_void", "void invoices cannot be edited")
	case inv.AmountPaid.IsPositive() || inv.Status == "paid" || inv.Status == "partially_paid":
		return apperr.Unprocessable("invoice_locked", "invoices with payments cannot be edited; void it and issue a new one after reversing payments")
	}
	return nil
}

func checkVersion(want *int32, have int32) error {
	if want != nil && *want != have {
		return apperr.Conflict("stale_version", "this invoice was changed by someone else; reload it and try again")
	}
	return nil
}

func (s *Service) Update(ctx context.Context, p authz.Principal, id uuid.UUID, in Input) (Invoice, error) {
	if err := p.Require(authz.InvoicesManage); err != nil {
		return Invoice{}, err
	}
	var out Invoice
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetInvoiceForUpdate(ctx, id)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("invoice")
		}
		if err != nil {
			return err
		}
		if err := editable(cur); err != nil {
			return err
		}
		if err := checkVersion(in.Version, cur.Version); err != nil {
			return err
		}
		before, err := load(ctx, q, id, p)
		if err != nil {
			return err
		}
		d, _, err := resolve(ctx, q, in, cur.Currency)
		if err != nil {
			return err
		}
		if cur.Status == "sent" && d.Totals.Total.IsZero() {
			return (&apperr.Validation{}).With("items", "a sent invoice must have a total greater than zero")
		}
		if err := q.DeleteInvoiceItems(ctx, id); err != nil {
			return err
		}
		if _, err := q.UpdateInvoiceTerms(ctx, store.UpdateInvoiceTermsParams{
			ID: id, CustomerID: d.CustomerID, ProjectID: d.ProjectID, Currency: d.Currency, IssueDate: d.IssueDate, DueDate: d.DueDate,
			DiscountType: string(d.DiscountType), DiscountValue: d.DiscountValue, TaxRate: d.TaxRate, Subtotal: d.Totals.Subtotal,
			DiscountTotal: d.Totals.DiscountTotal, TaxTotal: d.Totals.TaxTotal, Total: d.Totals.Total, Notes: d.Notes,
		}); err != nil {
			return err
		}
		if err := writeItems(ctx, q, p.Org.ID, id, d); err != nil {
			return err
		}
		out, err = load(ctx, q, id, p)
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "invoice.updated", EntityType: "invoice", EntityID: id,
			Before: snapshot(before), After: snapshot(out)})
	})
	return out, err
}

func (s *Service) Send(ctx context.Context, p authz.Principal, id uuid.UUID, version *int32) (Invoice, error) {
	if err := p.Require(authz.InvoicesManage); err != nil {
		return Invoice{}, err
	}
	var out Invoice
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetInvoiceForUpdate(ctx, id)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("invoice")
		}
		if err != nil {
			return err
		}
		if err := checkVersion(version, cur.Version); err != nil {
			return err
		}
		if cur.Status != "draft" {
			return apperr.Unprocessable("invalid_status", "only draft invoices can be sent")
		}
		if !cur.Total.IsPositive() {
			return apperr.Unprocessable("zero_total", "an invoice must have a total greater than zero before it is sent")
		}
		if _, err := q.MarkInvoiceSent(ctx, id); err != nil {
			return err
		}
		out, err = load(ctx, q, id, p)
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "invoice.sent", EntityType: "invoice", EntityID: id,
			Before: map[string]any{"status": cur.Status}, After: map[string]any{"status": out.Status}})
	})
	return out, err
}

func (s *Service) Void(ctx context.Context, p authz.Principal, id uuid.UUID, reason string, version *int32) (Invoice, error) {
	if err := p.Require(authz.InvoicesManage); err != nil {
		return Invoice{}, err
	}
	var v apperr.Validation
	reason = input.Text(&v, "reason", reason, 500)
	if err := v.Err(); err != nil {
		return Invoice{}, err
	}
	var out Invoice
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetInvoiceForUpdate(ctx, id)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("invoice")
		}
		if err != nil {
			return err
		}
		if err := checkVersion(version, cur.Version); err != nil {
			return err
		}
		if cur.Status == "void" {
			return apperr.Unprocessable("invoice_void", "this invoice is already void")
		}
		if cur.AmountPaid.IsPositive() {
			return apperr.Unprocessable("has_payments", "reverse the payments on this invoice before voiding it")
		}
		if _, err := q.VoidInvoice(ctx, store.VoidInvoiceParams{ID: id, VoidReason: &reason}); err != nil {
			return err
		}
		out, err = load(ctx, q, id, p)
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "invoice.voided", EntityType: "invoice", EntityID: id,
			Before: map[string]any{"status": cur.Status, "total": money.Format(cur.Total, cur.Currency)},
			After:  map[string]any{"status": "void", "reason": reason}})
	})
	return out, err
}

func load(ctx context.Context, q *store.Queries, id uuid.UUID, p authz.Principal) (Invoice, error) {
	row, err := q.GetInvoice(ctx, id)
	if apperr.IsNoRows(err) {
		return Invoice{}, apperr.NotFound("invoice")
	}
	if err != nil {
		return Invoice{}, err
	}
	inv := invoiceFromStore(row, p.Today())
	items, err := q.ListInvoiceItems(ctx, id)
	if err != nil {
		return Invoice{}, err
	}
	for _, it := range items {
		inv.Items = append(inv.Items, itemFromStore(it, row.Currency))
	}
	pays, err := q.ListInvoicePayments(ctx, id)
	if err != nil {
		return Invoice{}, err
	}
	for _, pr := range pays {
		pay := paymentFromStore(store.Payment{ID: pr.ID, InvoiceID: pr.InvoiceID, Amount: pr.Amount, Currency: pr.Currency,
			PaidOn: pr.PaidOn, Method: pr.Method, Reference: pr.Reference, Status: pr.Status, ReversedAt: pr.ReversedAt,
			ReversalReason: pr.ReversalReason, Source: pr.Source, CreatedAt: pr.CreatedAt})
		pay.RecordedByName = pr.RecordedByName
		inv.Payments = append(inv.Payments, pay)
	}
	return inv, nil
}

func (s *Service) Get(ctx context.Context, p authz.Principal, id uuid.UUID) (Invoice, error) {
	if err := p.Require(authz.InvoicesView); err != nil {
		return Invoice{}, err
	}
	var out Invoice
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		var err error
		out, err = load(ctx, store.New(tx), id, p)
		return err
	})
	return out, err
}

type ListResult struct {
	Data  []ListItem `json:"data"`
	Total int64      `json:"total"`
}

func (s *Service) List(ctx context.Context, p authz.Principal, f store.ListInvoicesParams) (ListResult, error) {
	if err := p.Require(authz.InvoicesView); err != nil {
		return ListResult{}, err
	}
	f.Today = p.Today()
	var res ListResult
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		rows, err := q.ListInvoices(ctx, f)
		if err != nil {
			return err
		}
		res.Total, err = q.CountInvoices(ctx, store.CountInvoicesParams{Today: f.Today, Status: f.Status, CustomerID: f.CustomerID,
			ProjectID: f.ProjectID, Pattern: f.Pattern, From: f.From, To: f.To})
		if err != nil {
			return err
		}
		res.Data = make([]ListItem, 0, len(rows))
		for _, r := range rows {
			res.Data = append(res.Data, ListItem{ID: r.ID, Number: r.Number, Status: r.Status, DisplayStatus: displayStatus(r.Status, r.IsOverdue),
				IsOverdue: r.IsOverdue, CustomerID: r.CustomerID, CustomerName: r.CustomerName, ProjectID: r.ProjectID, ProjectName: r.ProjectName,
				Currency: r.Currency, IssueDate: input.FormatDate(r.IssueDate), DueDate: input.FormatDate(r.DueDate),
				Total: money.Format(r.Total, r.Currency), AmountPaid: money.Format(r.AmountPaid, r.Currency),
				Balance: money.Format(r.Total.Sub(r.AmountPaid), r.Currency), Source: r.Source})
		}
		return nil
	})
	return res, err
}
