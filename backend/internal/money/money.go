// Package money holds the monetary rules of the application. All arithmetic uses
// shopspring/decimal; no amount is ever represented as a float.
package money

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/shopspring/decimal"
)

// StoragePlaces is the scale of NUMERIC(19,4) columns. MaxIntegerDigits keeps values inside them.
const (
	StoragePlaces    = 4
	MaxIntegerDigits = 15
)

// minorUnits mirrors the currencies reference table (ISO 4217 exponents).
var minorUnits = map[string]int32{
	"USD": 2, "EUR": 2, "GBP": 2, "CAD": 2, "AUD": 2, "NZD": 2, "CHF": 2, "SGD": 2, "INR": 2,
	"JPY": 0, "KWD": 3,
}

func Supported(code string) bool { _, ok := minorUnits[code]; return ok }

func SupportedCodes() []string {
	out := make([]string, 0, len(minorUnits))
	for c := range minorUnits {
		out = append(out, c)
	}
	return out
}

// MinorUnits returns the number of decimal places for a currency (2 if unknown).
func MinorUnits(code string) int32 {
	if n, ok := minorUnits[code]; ok {
		return n
	}
	return 2
}

// Round rounds half away from zero ("commercial rounding"): 2.345 -> 2.35, -2.345 -> -2.35.
func Round(d decimal.Decimal, places int32) decimal.Decimal { return d.Round(places) }

// RoundCurrency rounds to the currency's minor units.
func RoundCurrency(d decimal.Decimal, currency string) decimal.Decimal {
	return Round(d, MinorUnits(currency))
}

// Format renders an amount with exactly the currency's number of decimals, e.g. "1250.00".
func Format(d decimal.Decimal, currency string) string { return d.StringFixed(MinorUnits(currency)) }

// HasAtMostPlaces reports whether d needs no more than the given number of decimal places.
func HasAtMostPlaces(d decimal.Decimal, places int32) bool { return d.Equal(d.Truncate(places)) }

var decimalPattern = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

var (
	ErrEmpty     = errors.New("is required")
	ErrFormat    = errors.New("must be a decimal number such as 1250.00")
	ErrTooLarge  = errors.New("is too large")
	ErrPrecision = errors.New("has too many decimal places")
)

// Parse reads a plain decimal string. Exponents, separators and currency symbols are rejected so
// that what the user typed is exactly what is stored.
func Parse(s string, maxPlaces int32) (decimal.Decimal, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return decimal.Zero, ErrEmpty
	}
	if !decimalPattern.MatchString(s) {
		return decimal.Zero, ErrFormat
	}
	intPart := strings.TrimLeft(strings.SplitN(strings.TrimPrefix(s, "-"), ".", 2)[0], "0")
	if len(intPart) > MaxIntegerDigits {
		return decimal.Zero, ErrTooLarge
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, ErrFormat
	}
	if !HasAtMostPlaces(d, maxPlaces) {
		return decimal.Zero, ErrPrecision
	}
	return d, nil
}

// ParseAmount parses a monetary amount in a currency: positive, within the currency's minor units.
func ParseAmount(s, currency string) (decimal.Decimal, error) {
	d, err := Parse(s, MinorUnits(currency))
	if errors.Is(err, ErrPrecision) {
		return d, fmt.Errorf("can have at most %d decimal places for %s", MinorUnits(currency), currency)
	}
	if err != nil {
		return d, err
	}
	if !d.IsPositive() {
		return d, errors.New("must be greater than zero")
	}
	return d, nil
}

// Percent calculates part/whole*100 rounded to one decimal place; zero when whole is zero.
func Percent(part, whole decimal.Decimal) decimal.Decimal {
	if whole.IsZero() {
		return decimal.Zero
	}
	return part.Mul(decimal.NewFromInt(100)).DivRound(whole, 4).Round(1)
}
