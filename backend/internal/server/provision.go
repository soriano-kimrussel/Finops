package server

import (
	"context"

	"github.com/google/uuid"

	"finopshq/internal/store"
)

// provisionOrganization seeds per-organization defaults when an organization is created.
func provisionOrganization(_ context.Context, _ *store.Queries, _ uuid.UUID) error {
	return nil
}
