package money

import (
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestRoundHalfAwayFromZero(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2.345", "2.35"}, {"2.344", "2.34"}, {"2.355", "2.36"}, {"-2.345", "-2.35"}, {"0.005", "0.01"}, {"0.004999", "0"},
	}
	for _, c := range cases {
		if got := Round(d(c.in), 2); !got.Equal(d(c.want)) {
			t.Errorf("Round(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestCurrencyPrecision(t *testing.T) {
	if got := RoundCurrency(d("1234.5"), "JPY"); !got.Equal(d("1235")) {
		t.Errorf("JPY rounding: %s", got)
	}
	if got := RoundCurrency(d("1.23456"), "KWD"); !got.Equal(d("1.235")) {
		t.Errorf("KWD rounding: %s", got)
	}
	if got := Format(d("10"), "USD"); got != "10.00" {
		t.Errorf("Format USD: %s", got)
	}
	if got := Format(d("10"), "JPY"); got != "10" {
		t.Errorf("Format JPY: %s", got)
	}
	if got := Format(d("0.1").Add(d("0.2")), "USD"); got != "0.30" {
		t.Errorf("0.1+0.2 must be exactly 0.30, got %s", got)
	}
}

func TestParse(t *testing.T) {
	ok := map[string]string{"100": "100", "100.5": "100.5", "0.0001": "0.0001", " 42.10 ": "42.1", "-3.25": "-3.25"}
	for in, want := range ok {
		got, err := Parse(in, 4)
		if err != nil || !got.Equal(d(want)) {
			t.Errorf("Parse(%q) = %s, %v", in, got, err)
		}
	}
	bad := map[string]error{
		"":                   ErrEmpty,
		"1e5":                ErrFormat,
		"1,000.00":           ErrFormat,
		"$10":                ErrFormat,
		"10.":                ErrFormat,
		"1.00001":            ErrPrecision,
		"1234567890123456.0": ErrTooLarge,
	}
	for in, want := range bad {
		if _, err := Parse(in, 4); err != want {
			t.Errorf("Parse(%q) error = %v, want %v", in, err, want)
		}
	}
}

func TestParseAmount(t *testing.T) {
	if _, err := ParseAmount("10.005", "USD"); err == nil {
		t.Error("USD amount with 3 decimals must fail")
	}
	if _, err := ParseAmount("10.005", "KWD"); err != nil {
		t.Errorf("KWD amount with 3 decimals must pass: %v", err)
	}
	if _, err := ParseAmount("10.5", "JPY"); err == nil {
		t.Error("JPY amount with decimals must fail")
	}
	for _, in := range []string{"0", "-5", "0.00"} {
		if _, err := ParseAmount(in, "USD"); err == nil {
			t.Errorf("ParseAmount(%q) must reject non-positive values", in)
		}
	}
}

func TestPercent(t *testing.T) {
	if got := Percent(d("1"), d("3")); !got.Equal(d("33.3")) {
		t.Errorf("1/3 = %s", got)
	}
	if got := Percent(d("2"), d("3")); !got.Equal(d("66.7")) {
		t.Errorf("2/3 = %s", got)
	}
	if got := Percent(d("5"), d("0")); !got.IsZero() {
		t.Errorf("x/0 = %s", got)
	}
}
