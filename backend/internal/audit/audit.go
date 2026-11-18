package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"finopshq/internal/httpx"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

type Entry struct {
	Action     string
	EntityType string
	EntityID   uuid.UUID
	Before     any
	After      any
	Extra      map[string]any
}

// Record writes an audit row inside the caller's transaction, so the audit entry commits
// or rolls back together with the change it describes. A zero UserID records a system actor.
func Record(ctx context.Context, q *store.Queries, s tenant.Scope, e Entry) error {
	changes := map[string]any{}
	if e.Before != nil {
		changes["before"] = e.Before
	}
	if e.After != nil {
		changes["after"] = e.After
	}
	for k, v := range e.Extra {
		changes[k] = v
	}
	raw, err := json.Marshal(changes)
	if err != nil {
		return fmt.Errorf("marshal audit changes: %w", err)
	}

	params := store.InsertAuditLogParams{
		OrganizationID: s.OrgID,
		ActorType:      "system",
		Action:         e.Action,
		EntityType:     e.EntityType,
		Changes:        raw,
		RequestID:      nonEmpty(httpx.RequestID(ctx)),
		Ip:             nonEmpty(httpx.ClientIPFromContext(ctx)),
	}
	if s.UserID != uuid.Nil {
		uid := s.UserID
		params.ActorUserID = &uid
		params.ActorType = "user"
	}
	if e.EntityID != uuid.Nil {
		id := e.EntityID
		params.EntityID = &id
	}
	if err := q.InsertAuditLog(ctx, params); err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return nil
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
