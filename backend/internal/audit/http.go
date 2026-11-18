package audit

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"finopshq/internal/apperr"
	"finopshq/internal/authz"
	"finopshq/internal/httpx"
	"finopshq/internal/input"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

type Handler struct{ pool *pgxpool.Pool }

func NewHandler(pool *pgxpool.Pool) *Handler { return &Handler{pool: pool} }

type Log struct {
	ID         uuid.UUID       `json:"id"`
	Action     string          `json:"action"`
	EntityType string          `json:"entity_type"`
	EntityID   *uuid.UUID      `json:"entity_id"`
	ActorType  string          `json:"actor_type"`
	ActorName  *string         `json:"actor_name"`
	ActorEmail *string         `json:"actor_email"`
	Changes    json.RawMessage `json:"changes"`
	RequestID  *string         `json:"request_id"`
	IP         *string         `json:"ip"`
	CreatedAt  time.Time       `json:"created_at"`
}

// List pages through audit history newest first. The cursor is "<RFC3339Nano>_<id>" of the
// last row seen, which stays stable while new entries are being written.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p := authz.MustPrincipal(r.Context())
	if err := p.Require(authz.AuditView); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	params := store.ListAuditLogsParams{OrganizationID: p.Org.ID, RowLimit: 50}
	params.EntityType = input.QueryString(r, "entity_type")
	params.Action = input.QueryString(r, "action")
	var err error
	if params.EntityID, err = input.QueryUUID(r, "entity_id"); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if params.ActorUserID, err = input.QueryUUID(r, "actor_user_id"); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	from, to, err := input.DateRange(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if from != nil {
		params.From = from
	}
	if to != nil {
		end := to.AddDate(0, 0, 1)
		params.To = &end
	}
	if c := r.URL.Query().Get("cursor"); c != "" {
		ts, id, ok := strings.Cut(c, "_")
		t, terr := time.Parse(time.RFC3339Nano, ts)
		uid, uerr := uuid.Parse(id)
		if !ok || terr != nil || uerr != nil {
			httpx.WriteError(w, r, apperr.BadRequest("invalid cursor"))
			return
		}
		params.BeforeCreatedAt, params.BeforeID = &t, &uid
	}

	var out []Log
	err = tenant.WithTx(r.Context(), h.pool, p.Scope(), func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListAuditLogs(r.Context(), params)
		if err != nil {
			return err
		}
		out = make([]Log, 0, len(rows))
		for _, a := range rows {
			out = append(out, Log{ID: a.ID, Action: a.Action, EntityType: a.EntityType, EntityID: a.EntityID, ActorType: a.ActorType,
				ActorName: a.ActorName, ActorEmail: a.ActorEmail, Changes: a.Changes, RequestID: a.RequestID, IP: a.Ip, CreatedAt: a.CreatedAt})
		}
		return nil
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	var next *string
	if len(out) == int(params.RowLimit) {
		last := out[len(out)-1]
		c := last.CreatedAt.Format(time.RFC3339Nano) + "_" + last.ID.String()
		next = &c
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out, "next_cursor": next})
}
