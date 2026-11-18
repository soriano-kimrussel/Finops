package budget

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestAssessThresholds(t *testing.T) {
	cases := []struct {
		budget, actual string
		level          Level
		pct            string
	}{
		{"1000", "0", LevelOK, "0"},
		{"1000", "799.99", LevelOK, "80"},
		{"1000", "800", LevelWarning, "80"},
		{"1000", "1000", LevelWarning, "100"},
		{"1000", "1000.01", LevelOver, "100"},
		{"1000", "1500", LevelOver, "150"},
		{"0", "0", LevelNone, "0"},
		{"0", "10", LevelOver, "0"},
	}
	for _, c := range cases {
		a := Assess(d(c.budget), d(c.actual), decimal.Zero)
		if a.Level != c.level || !a.PercentUsed.Equal(d(c.pct)) {
			t.Errorf("Assess(%s, %s) = %s %s%%, want %s %s%%", c.budget, c.actual, a.Level, a.PercentUsed, c.level, c.pct)
		}
	}
}

func TestAssessRemainingAndPending(t *testing.T) {
	a := Assess(d("5000.00"), d("3200.50"), d("900"))
	if !a.Remaining.Equal(d("1799.50")) {
		t.Errorf("remaining = %s", a.Remaining)
	}
	if a.Level != LevelOK {
		t.Errorf("pending spend must not count toward consumption, got %s", a.Level)
	}
}

func TestAllocateRoutesSpendToLines(t *testing.T) {
	travel, software, hardware := uuid.New(), uuid.New(), uuid.New()
	travelLine, generalLine := uuid.New(), uuid.New()
	lines := []Line{
		{ID: travelLine, CategoryID: &travel, Amount: d("2000")},
		{ID: generalLine, Amount: d("3000")},
	}
	spend := []Spend{
		{CategoryID: travel, Approved: d("1500"), Pending: d("100")},
		{CategoryID: software, Approved: d("400")},
		{CategoryID: hardware, Approved: d("250.25"), Pending: d("50")},
	}
	a := Allocate(lines, spend)
	if !a.ByLine[travelLine].Approved.Equal(d("1500")) {
		t.Errorf("travel line = %s", a.ByLine[travelLine].Approved)
	}
	if !a.ByLine[generalLine].Approved.Equal(d("650.25")) || !a.ByLine[generalLine].Pending.Equal(d("50")) {
		t.Errorf("general line = %+v", a.ByLine[generalLine])
	}
	if !a.TotalBudget.Equal(d("5000")) || !a.Total.Approved.Equal(d("2150.25")) {
		t.Errorf("totals = %s / %s", a.TotalBudget, a.Total.Approved)
	}
	if !a.Unbudgeted.Approved.IsZero() {
		t.Error("nothing should be unbudgeted when a general line exists")
	}
}

func TestAllocateWithoutGeneralLine(t *testing.T) {
	travel, other := uuid.New(), uuid.New()
	line := uuid.New()
	a := Allocate([]Line{{ID: line, CategoryID: &travel, Amount: d("100")}}, []Spend{{CategoryID: other, Approved: d("75")}})
	if !a.Unbudgeted.Approved.Equal(d("75")) || !a.ByLine[line].Approved.IsZero() {
		t.Errorf("unexpected allocation %+v", a)
	}
}
