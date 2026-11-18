// Package ledger records cash movements. Each source event (payment, reversal, approved expense)
// posts exactly one immutable row, enforced by a unique key on (source_type, source_id).
package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"finopshq/internal/store"
)

const CashAccountCode = "1000"

var DefaultAccounts = []struct{ Code, Name, Type string }{
	{"1000", "Operating Cash", "asset"},
	{"1200", "Accounts Receivable", "asset"},
	{"4000", "Service Revenue", "revenue"},
	{"6000", "Operating Expenses", "expense"},
}

type Entry struct {
	OrgID       uuid.UUID
	OccurredOn  time.Time
	Direction   string
	Amount      decimal.Decimal
	Currency    string
	Description string
	SourceType  string
	SourceID    uuid.UUID
}

func Post(ctx context.Context, q *store.Queries, e Entry) error {
	acct, err := q.GetAccountByCode(ctx, store.GetAccountByCodeParams{OrganizationID: e.OrgID, Code: CashAccountCode})
	if err != nil {
		return fmt.Errorf("load cash account: %w", err)
	}
	return q.InsertTransaction(ctx, store.InsertTransactionParams{
		OrganizationID: e.OrgID,
		AccountID:      acct.ID,
		OccurredOn:     e.OccurredOn,
		Direction:      e.Direction,
		Amount:         e.Amount,
		Currency:       e.Currency,
		Description:    e.Description,
		SourceType:     e.SourceType,
		SourceID:       e.SourceID,
	})
}
