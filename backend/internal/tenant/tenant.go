package tenant

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Scope identifies whose data a transaction may touch. RLS policies read these values.
// A zero OrgID means "no tenant": tenant tables then return no rows.
type Scope struct {
	OrgID  uuid.UUID
	UserID uuid.UUID
}

// WithTx runs fn in a transaction with the tenant context set transaction-locally, so the
// setting disappears at COMMIT/ROLLBACK and never leaks to the next user of a pooled connection.
func WithTx(ctx context.Context, pool *pgxpool.Pool, s Scope, fn func(tx pgx.Tx) error) error {
	return pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := Apply(ctx, tx, s); err != nil {
			return err
		}
		return fn(tx)
	})
}

func Apply(ctx context.Context, tx pgx.Tx, s Scope) error {
	_, err := tx.Exec(ctx,
		"SELECT set_config('app.org_id', $1, true), set_config('app.user_id', $2, true)",
		idString(s.OrgID), idString(s.UserID))
	if err != nil {
		return fmt.Errorf("set tenant context: %w", err)
	}
	return nil
}

func idString(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}
