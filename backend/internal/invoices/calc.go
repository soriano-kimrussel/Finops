package invoices

import (
	"errors"

	"github.com/shopspring/decimal"

	"finopshq/internal/money"
)

type DiscountType string

const (
	DiscountNone    DiscountType = "none"
	DiscountPercent DiscountType = "percent"
	DiscountFixed   DiscountType = "fixed"
)

type Line struct {
	Description string
	Quantity    decimal.Decimal
	UnitPrice   decimal.Decimal
}

type Totals struct {
	LineTotals    []decimal.Decimal
	Subtotal      decimal.Decimal
	DiscountTotal decimal.Decimal
	TaxableAmount decimal.Decimal
	TaxTotal      decimal.Decimal
	Total         decimal.Decimal
}

var hundred = decimal.NewFromInt(100)

var ErrDiscountExceedsSubtotal = errors.New("discount cannot exceed the subtotal")

// Compute applies the invoice rules, in order:
//  1. each line total = quantity × unit price, rounded to the currency's minor units;
//  2. subtotal = sum of rounded line totals;
//  3. discount = percent of subtotal (rounded) or a fixed amount, never more than the subtotal;
//  4. tax = tax rate × (subtotal − discount), rounded once;
//  5. total = subtotal − discount + tax.
//
// Rounding is half away from zero. Inputs are assumed validated (positive quantities, etc.).
func Compute(lines []Line, discountType DiscountType, discountValue, taxRate decimal.Decimal, currency string) (Totals, error) {
	places := money.MinorUnits(currency)
	t := Totals{LineTotals: make([]decimal.Decimal, len(lines))}

	for i, l := range lines {
		lt := money.Round(l.Quantity.Mul(l.UnitPrice), places)
		t.LineTotals[i] = lt
		t.Subtotal = t.Subtotal.Add(lt)
	}

	switch discountType {
	case DiscountPercent:
		t.DiscountTotal = money.Round(t.Subtotal.Mul(discountValue).Div(hundred), places)
	case DiscountFixed:
		t.DiscountTotal = discountValue
	default:
		t.DiscountTotal = decimal.Zero
	}
	if t.DiscountTotal.GreaterThan(t.Subtotal) {
		return Totals{}, ErrDiscountExceedsSubtotal
	}

	t.TaxableAmount = t.Subtotal.Sub(t.DiscountTotal)
	t.TaxTotal = money.Round(t.TaxableAmount.Mul(taxRate).Div(hundred), places)
	t.Total = t.TaxableAmount.Add(t.TaxTotal)
	return t, nil
}
