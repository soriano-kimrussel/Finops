package invoices

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"finopshq/internal/apperr"
	"finopshq/internal/input"
	"finopshq/internal/money"
	"finopshq/internal/store"
)

const maxItems = 200

var maxQuantity = decimal.NewFromInt(99_999_999)

type Item struct {
	ID          uuid.UUID `json:"id"`
	Position    int32     `json:"position"`
	Description string    `json:"description"`
	Quantity    string    `json:"quantity"`
	UnitPrice   string    `json:"unit_price"`
	LineTotal   string    `json:"line_total"`
}

type Payment struct {
	ID             uuid.UUID  `json:"id"`
	InvoiceID      uuid.UUID  `json:"invoice_id"`
	InvoiceNumber  string     `json:"invoice_number,omitempty"`
	CustomerID     *uuid.UUID `json:"customer_id,omitempty"`
	CustomerName   string     `json:"customer_name,omitempty"`
	Amount         string     `json:"amount"`
	Currency       string     `json:"currency"`
	PaidOn         string     `json:"paid_on"`
	Method         string     `json:"method"`
	Reference      *string    `json:"reference"`
	Status         string     `json:"status"`
	RecordedByName *string    `json:"recorded_by_name,omitempty"`
	ReversedAt     *time.Time `json:"reversed_at,omitempty"`
	ReversalReason *string    `json:"reversal_reason,omitempty"`
	Source         string     `json:"source"`
	CreatedAt      time.Time  `json:"created_at"`
}

type Invoice struct {
	ID            uuid.UUID  `json:"id"`
	Number        string     `json:"number"`
	Status        string     `json:"status"`
	DisplayStatus string     `json:"display_status"`
	IsOverdue     bool       `json:"is_overdue"`
	CustomerID    uuid.UUID  `json:"customer_id"`
	CustomerName  string     `json:"customer_name"`
	CustomerEmail *string    `json:"customer_email,omitempty"`
	ProjectID     *uuid.UUID `json:"project_id"`
	ProjectName   *string    `json:"project_name"`
	Currency      string     `json:"currency"`
	IssueDate     string     `json:"issue_date"`
	DueDate       string     `json:"due_date"`
	DiscountType  string     `json:"discount_type"`
	DiscountValue string     `json:"discount_value"`
	TaxRate       string     `json:"tax_rate"`
	Subtotal      string     `json:"subtotal"`
	DiscountTotal string     `json:"discount_total"`
	TaxTotal      string     `json:"tax_total"`
	Total         string     `json:"total"`
	AmountPaid    string     `json:"amount_paid"`
	Balance       string     `json:"balance"`
	Notes         *string    `json:"notes"`
	SentAt        *time.Time `json:"sent_at"`
	VoidedAt      *time.Time `json:"voided_at"`
	VoidReason    *string    `json:"void_reason"`
	Source        string     `json:"source"`
	ExternalID    *string    `json:"external_id"`
	Version       int32      `json:"version"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Items         []Item     `json:"items"`
	Payments      []Payment  `json:"payments"`
}

type ListItem struct {
	ID            uuid.UUID  `json:"id"`
	Number        string     `json:"number"`
	Status        string     `json:"status"`
	DisplayStatus string     `json:"display_status"`
	IsOverdue     bool       `json:"is_overdue"`
	CustomerID    uuid.UUID  `json:"customer_id"`
	CustomerName  string     `json:"customer_name"`
	ProjectID     *uuid.UUID `json:"project_id"`
	ProjectName   *string    `json:"project_name"`
	Currency      string     `json:"currency"`
	IssueDate     string     `json:"issue_date"`
	DueDate       string     `json:"due_date"`
	Total         string     `json:"total"`
	AmountPaid    string     `json:"amount_paid"`
	Balance       string     `json:"balance"`
	Source        string     `json:"source"`
}

func displayStatus(status string, overdue bool) string {
	if overdue {
		return "overdue"
	}
	return status
}

// IsOverdue: an invoice is overdue when money is still owed after its due date, judged in the
// organization's time zone. Drafts and void invoices are never overdue.
func IsOverdue(status string, due, today time.Time) bool {
	return (status == "sent" || status == "partially_paid") && due.Before(today)
}

// formatPrice keeps sub-cent unit prices visible while showing whole prices with currency decimals.
func formatPrice(d decimal.Decimal, currency string) string {
	if money.HasAtMostPlaces(d, money.MinorUnits(currency)) {
		return money.Format(d, currency)
	}
	return d.String()
}

func invoiceFromStore(r store.GetInvoiceRow, today time.Time) Invoice {
	c := r.Currency
	overdue := IsOverdue(r.Status, r.DueDate, today)
	return Invoice{
		ID: r.ID, Number: r.Number, Status: r.Status, DisplayStatus: displayStatus(r.Status, overdue), IsOverdue: overdue,
		CustomerID: r.CustomerID, CustomerName: r.CustomerName, CustomerEmail: r.CustomerEmail,
		ProjectID: r.ProjectID, ProjectName: r.ProjectName, Currency: c,
		IssueDate: input.FormatDate(r.IssueDate), DueDate: input.FormatDate(r.DueDate),
		DiscountType: r.DiscountType, DiscountValue: r.DiscountValue.String(), TaxRate: r.TaxRate.String(),
		Subtotal: money.Format(r.Subtotal, c), DiscountTotal: money.Format(r.DiscountTotal, c), TaxTotal: money.Format(r.TaxTotal, c),
		Total: money.Format(r.Total, c), AmountPaid: money.Format(r.AmountPaid, c), Balance: money.Format(r.Total.Sub(r.AmountPaid), c),
		Notes: r.Notes, SentAt: r.SentAt, VoidedAt: r.VoidedAt, VoidReason: r.VoidReason, Source: r.Source, ExternalID: r.ExternalID,
		Version: r.Version, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Items: []Item{}, Payments: []Payment{},
	}
}

func itemFromStore(i store.InvoiceItem, currency string) Item {
	return Item{ID: i.ID, Position: i.Position, Description: i.Description, Quantity: i.Quantity.String(),
		UnitPrice: formatPrice(i.UnitPrice, currency), LineTotal: money.Format(i.LineTotal, currency)}
}

func paymentFromStore(p store.Payment) Payment {
	return Payment{ID: p.ID, InvoiceID: p.InvoiceID, Amount: money.Format(p.Amount, p.Currency), Currency: p.Currency,
		PaidOn: input.FormatDate(p.PaidOn), Method: p.Method, Reference: p.Reference, Status: p.Status, ReversedAt: p.ReversedAt,
		ReversalReason: p.ReversalReason, Source: p.Source, CreatedAt: p.CreatedAt}
}

type ItemInput struct {
	Description string `json:"description"`
	Quantity    string `json:"quantity"`
	UnitPrice   string `json:"unit_price"`
}

type Input struct {
	CustomerID    string      `json:"customer_id"`
	ProjectID     *string     `json:"project_id"`
	Currency      string      `json:"currency"`
	IssueDate     string      `json:"issue_date"`
	DueDate       string      `json:"due_date"`
	DiscountType  string      `json:"discount_type"`
	DiscountValue string      `json:"discount_value"`
	TaxRate       string      `json:"tax_rate"`
	Notes         *string     `json:"notes"`
	Items         []ItemInput `json:"items"`
	Version       *int32      `json:"version"`
}

// draft is a fully validated, fully calculated invoice ready to be written.
type draft struct {
	CustomerID    uuid.UUID
	ProjectID     *uuid.UUID
	Currency      string
	IssueDate     time.Time
	DueDate       time.Time
	DiscountType  DiscountType
	DiscountValue decimal.Decimal
	TaxRate       decimal.Decimal
	Notes         *string
	Lines         []Line
	Totals        Totals
}

func fieldErr(err error) string {
	if err == money.ErrPrecision {
		return "has more than 4 decimal places"
	}
	return err.Error()
}

// parseInput validates everything that does not need the database. currency must already be
// resolved (from the input or the customer's default).
func parseInput(in Input, currency string, v *apperr.Validation) draft {
	d := draft{
		ProjectID: input.OptionalUUID(v, "project_id", in.ProjectID),
		Currency:  currency,
		IssueDate: input.Date(v, "issue_date", in.IssueDate),
		DueDate:   input.Date(v, "due_date", in.DueDate),
		Notes:     input.OptionalText(v, "notes", in.Notes, 2000),
	}
	if !money.Supported(currency) {
		v.Add("currency", "is not a supported currency")
	}
	if !v.Has("issue_date") && !v.Has("due_date") && d.DueDate.Before(d.IssueDate) {
		v.Add("due_date", "must be on or after the issue date")
	}

	switch DiscountType(strings.TrimSpace(in.DiscountType)) {
	case "", DiscountNone:
		d.DiscountType = DiscountNone
	case DiscountPercent:
		d.DiscountType = DiscountPercent
		val, err := money.Parse(in.DiscountValue, 4)
		if err != nil {
			v.Add("discount_value", fieldErr(err))
		} else if val.IsNegative() || val.GreaterThan(decimal.NewFromInt(100)) {
			v.Add("discount_value", "must be between 0 and 100")
		}
		d.DiscountValue = val
	case DiscountFixed:
		d.DiscountType = DiscountFixed
		val, err := money.Parse(in.DiscountValue, money.MinorUnits(currency))
		if err != nil {
			v.Add("discount_value", fieldErr(err))
		} else if val.IsNegative() {
			v.Add("discount_value", "must not be negative")
		}
		d.DiscountValue = val
	default:
		v.Add("discount_type", "must be none, percent or fixed")
	}

	if strings.TrimSpace(in.TaxRate) == "" {
		d.TaxRate = decimal.Zero
	} else if rate, err := money.Parse(in.TaxRate, 4); err != nil {
		v.Add("tax_rate", fieldErr(err))
	} else if rate.IsNegative() || rate.GreaterThan(decimal.NewFromInt(100)) {
		v.Add("tax_rate", "must be between 0 and 100")
	} else {
		d.TaxRate = rate
	}

	if len(in.Items) == 0 {
		v.Add("items", "add at least one line item")
	}
	if len(in.Items) > maxItems {
		v.Add("items", fmt.Sprintf("an invoice can have at most %d line items", maxItems))
	}
	for i, it := range in.Items {
		f := func(name string) string { return fmt.Sprintf("items.%d.%s", i, name) }
		desc := input.Text(v, f("description"), it.Description, 500)
		q, err := money.Parse(it.Quantity, 4)
		if err != nil {
			v.Add(f("quantity"), fieldErr(err))
		} else if !q.IsPositive() {
			v.Add(f("quantity"), "must be greater than zero")
		} else if q.GreaterThan(maxQuantity) {
			v.Add(f("quantity"), "is too large")
		}
		p, err := money.Parse(it.UnitPrice, 4)
		if err != nil {
			v.Add(f("unit_price"), fieldErr(err))
		} else if p.IsNegative() {
			v.Add(f("unit_price"), "must not be negative")
		}
		d.Lines = append(d.Lines, Line{Description: desc, Quantity: q, UnitPrice: p})
	}

	if v.Err() == nil {
		t, err := Compute(d.Lines, d.DiscountType, d.DiscountValue, d.TaxRate, currency)
		if err != nil {
			v.Add("discount_value", err.Error())
		}
		limit := decimal.New(1, money.MaxIntegerDigits)
		if t.Total.GreaterThanOrEqual(limit) || t.Subtotal.GreaterThanOrEqual(limit) {
			v.Add("items", "invoice total is too large")
		}
		d.Totals = t
	}
	return d
}

type PreviewResult struct {
	Currency      string   `json:"currency"`
	LineTotals    []string `json:"line_totals"`
	Subtotal      string   `json:"subtotal"`
	DiscountTotal string   `json:"discount_total"`
	TaxableAmount string   `json:"taxable_amount"`
	TaxTotal      string   `json:"tax_total"`
	Total         string   `json:"total"`
}

func previewOf(t Totals, currency string) PreviewResult {
	lines := make([]string, len(t.LineTotals))
	for i, l := range t.LineTotals {
		lines[i] = money.Format(l, currency)
	}
	return PreviewResult{Currency: currency, LineTotals: lines, Subtotal: money.Format(t.Subtotal, currency),
		DiscountTotal: money.Format(t.DiscountTotal, currency), TaxableAmount: money.Format(t.TaxableAmount, currency),
		TaxTotal: money.Format(t.TaxTotal, currency), Total: money.Format(t.Total, currency)}
}
