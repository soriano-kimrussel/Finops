package server

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"finopshq/internal/qbsync"
	"finopshq/internal/reconcile"
	"finopshq/internal/tenant"
)

// Reconciler runs reconciliation after each successful sync job, as the system actor.
func Reconciler(pool *pgxpool.Pool) qbsync.AfterSync {
	svc := &reconcile.Service{Pool: pool}
	return func(ctx context.Context, orgID, _ uuid.UUID) (any, error) {
		return svc.Run(ctx, tenant.Scope{OrgID: orgID}, "sync")
	}
}
