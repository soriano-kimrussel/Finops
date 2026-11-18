// Package budget holds the deterministic budget-consumption rules.
package budget

import (
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"finopshq/internal/money"
)

type Level string

const (
	LevelNone    Level = "none"    // no budget set
	LevelOK      Level = "ok"      // below the warning threshold
	LevelWarning Level = "warning" // at or above 80% and not more than 100%
	LevelOver    Level = "over"    // more than 100% consumed
)

var warningThreshold = decimal.NewFromInt(80)
var full = decimal.NewFromInt(100)

type Assessment struct {
	Budget      decimal.Decimal
	Actual      decimal.Decimal
	Pending     decimal.Decimal
	Remaining   decimal.Decimal
	PercentUsed decimal.Decimal
	Level       Level
}

// Assess compares approved spending with a budget. Pending expenses are reported but do not
// count as consumed until they are approved.
func Assess(budgetAmount, actual, pending decimal.Decimal) Assessment {
	a := Assessment{Budget: budgetAmount, Actual: actual, Pending: pending, Remaining: budgetAmount.Sub(actual)}
	if !budgetAmount.IsPositive() {
		a.Level = LevelNone
		if actual.IsPositive() {
			a.Level = LevelOver
		}
		return a
	}
	a.PercentUsed = money.Percent(actual, budgetAmount)
	ratio := actual.Mul(full).Div(budgetAmount)
	switch {
	case ratio.GreaterThan(full):
		a.Level = LevelOver
	case ratio.GreaterThanOrEqual(warningThreshold):
		a.Level = LevelWarning
	default:
		a.Level = LevelOK
	}
	return a
}

type Line struct {
	ID         uuid.UUID
	CategoryID *uuid.UUID
	Amount     decimal.Decimal
}

type Spend struct {
	CategoryID uuid.UUID
	Approved   decimal.Decimal
	Pending    decimal.Decimal
}

type Allocation struct {
	ByLine      map[uuid.UUID]Spend
	Unbudgeted  Spend
	TotalBudget decimal.Decimal
	Total       Spend
}

// Allocate assigns spending to budget lines: a category's spend goes to its own line, otherwise
// to the general line (no category), otherwise it is reported as unbudgeted.
func Allocate(lines []Line, spend []Spend) Allocation {
	a := Allocation{ByLine: make(map[uuid.UUID]Spend, len(lines))}
	byCategory := map[uuid.UUID]uuid.UUID{}
	var general *uuid.UUID
	for _, l := range lines {
		a.TotalBudget = a.TotalBudget.Add(l.Amount)
		a.ByLine[l.ID] = Spend{}
		if l.CategoryID == nil {
			id := l.ID
			general = &id
		} else {
			byCategory[*l.CategoryID] = l.ID
		}
	}
	for _, s := range spend {
		a.Total.Approved = a.Total.Approved.Add(s.Approved)
		a.Total.Pending = a.Total.Pending.Add(s.Pending)
		lineID, ok := byCategory[s.CategoryID]
		if !ok && general != nil {
			lineID, ok = *general, true
		}
		if !ok {
			a.Unbudgeted.Approved = a.Unbudgeted.Approved.Add(s.Approved)
			a.Unbudgeted.Pending = a.Unbudgeted.Pending.Add(s.Pending)
			continue
		}
		cur := a.ByLine[lineID]
		cur.Approved = cur.Approved.Add(s.Approved)
		cur.Pending = cur.Pending.Add(s.Pending)
		a.ByLine[lineID] = cur
	}
	return a
}
