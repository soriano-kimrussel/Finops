package invoices

import (
	"testing"

	"github.com/shopspring/decimal"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func line(q, p string) Line { return Line{Description: "x", Quantity: dec(q), UnitPrice: dec(p)} }

func assertEq(t *testing.T, name string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(dec(want)) {
		t.Errorf("%s = %s, want %s", name, got, want)
	}
}

func TestSubtotalSumsRoundedLines(t *testing.T) {
	tot, err := Compute([]Line{line("3", "19.99"), line("1.5", "100"), line("0.333", "10")}, DiscountNone, dec("0"), dec("0"), "USD")
	if err != nil {
		t.Fatal(err)
	}
	assertEq(t, "line 0", tot.LineTotals[0], "59.97")
	assertEq(t, "line 1", tot.LineTotals[1], "150")
	assertEq(t, "line 2", tot.LineTotals[2], "3.33")
	assertEq(t, "subtotal", tot.Subtotal, "213.30")
	assertEq(t, "total", tot.Total, "213.30")
}

func TestLineRoundingHalfUp(t *testing.T) {
	tot, _ := Compute([]Line{line("1", "0.125"), line("1", "0.135")}, DiscountNone, dec("0"), dec("0"), "USD")
	assertEq(t, "0.125", tot.LineTotals[0], "0.13")
	assertEq(t, "0.135", tot.LineTotals[1], "0.14")
}

func TestTaxIsRoundedOnceOnDiscountedSubtotal(t *testing.T) {
	// Per-line tax would give 3 × round(0.0825×3.33)=3×0.27=0.81; invoice-level gives round(0.824175)=0.82.
	tot, err := Compute([]Line{line("1", "3.33"), line("1", "3.33"), line("1", "3.33")}, DiscountNone, dec("0"), dec("8.25"), "USD")
	if err != nil {
		t.Fatal(err)
	}
	assertEq(t, "tax", tot.TaxTotal, "0.82")
	assertEq(t, "total", tot.Total, "10.81")
}

func TestPercentDiscountThenTax(t *testing.T) {
	tot, err := Compute([]Line{line("10", "125.00")}, DiscountPercent, dec("12.5"), dec("20"), "GBP")
	if err != nil {
		t.Fatal(err)
	}
	assertEq(t, "subtotal", tot.Subtotal, "1250")
	assertEq(t, "discount", tot.DiscountTotal, "156.25")
	assertEq(t, "taxable", tot.TaxableAmount, "1093.75")
	assertEq(t, "tax", tot.TaxTotal, "218.75")
	assertEq(t, "total", tot.Total, "1312.50")
}

func TestPercentDiscountRounding(t *testing.T) {
	tot, _ := Compute([]Line{line("1", "99.99")}, DiscountPercent, dec("33.3333"), dec("0"), "USD")
	assertEq(t, "discount", tot.DiscountTotal, "33.33")
	assertEq(t, "total", tot.Total, "66.66")
}

func TestFixedDiscount(t *testing.T) {
	tot, err := Compute([]Line{line("2", "50")}, DiscountFixed, dec("25.50"), dec("10"), "USD")
	if err != nil {
		t.Fatal(err)
	}
	assertEq(t, "discount", tot.DiscountTotal, "25.50")
	assertEq(t, "tax", tot.TaxTotal, "7.45")
	assertEq(t, "total", tot.Total, "81.95")

	if _, err := Compute([]Line{line("1", "10")}, DiscountFixed, dec("10.01"), dec("0"), "USD"); err != ErrDiscountExceedsSubtotal {
		t.Fatalf("expected discount error, got %v", err)
	}
	full, err := Compute([]Line{line("1", "10")}, DiscountPercent, dec("100"), dec("10"), "USD")
	if err != nil || !full.Total.IsZero() {
		t.Fatalf("100%% discount should give zero total, got %v %v", full.Total, err)
	}
}

func TestZeroDecimalCurrency(t *testing.T) {
	tot, _ := Compute([]Line{line("3", "333.5")}, DiscountNone, dec("0"), dec("10"), "JPY")
	assertEq(t, "line", tot.LineTotals[0], "1001")
	assertEq(t, "tax", tot.TaxTotal, "100")
	assertEq(t, "total", tot.Total, "1101")
}

func TestTotalsIdentityHolds(t *testing.T) {
	tot, _ := Compute([]Line{line("7", "13.37"), line("0.25", "1999.99")}, DiscountPercent, dec("7.5"), dec("13.875"), "EUR")
	if !tot.Total.Equal(tot.Subtotal.Sub(tot.DiscountTotal).Add(tot.TaxTotal)) {
		t.Fatal("total must equal subtotal - discount + tax (the database enforces the same identity)")
	}
	for _, v := range []decimal.Decimal{tot.Subtotal, tot.DiscountTotal, tot.TaxTotal, tot.Total} {
		if !v.Equal(v.Round(2)) {
			t.Fatalf("%s has more than 2 decimals", v)
		}
	}
}
