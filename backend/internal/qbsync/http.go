package qbsync

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"finopshq/internal/apperr"
	"finopshq/internal/auth"
	"finopshq/internal/authz"
	"finopshq/internal/httpx"
	"finopshq/internal/input"
)

type Handler struct {
	svc            *Service
	frontendOrigin string
}

func NewHandler(s *Service, frontendOrigin string) *Handler {
	return &Handler{svc: s, frontendOrigin: frontendOrigin}
}

// OrgRoutes are mounted under /api/orgs/{orgID}.
func (h *Handler) OrgRoutes(r chi.Router) {
	r.Get("/integrations/quickbooks", httpx.Handle(func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		st, err := h.svc.Status(r.Context(), authz.MustPrincipal(r.Context()))
		return http.StatusOK, st, err
	}))
	r.Post("/integrations/quickbooks/connect", httpx.Handle(func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		res, err := h.svc.Connect(r.Context(), authz.MustPrincipal(r.Context()))
		return http.StatusOK, res, err
	}))
	r.Post("/integrations/quickbooks/disconnect", httpx.Handle(func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		return http.StatusNoContent, nil, h.svc.Disconnect(r.Context(), authz.MustPrincipal(r.Context()))
	}))
	r.Get("/sync-jobs", httpx.Handle(func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		status, err := input.QueryEnum(r, "status", "queued", "running", "retrying", "succeeded", "failed")
		if err != nil {
			return 0, nil, err
		}
		res, err := h.svc.ListJobs(r.Context(), authz.MustPrincipal(r.Context()), status, input.PageFrom(r))
		return http.StatusOK, res, err
	}))
	r.Post("/sync-jobs", httpx.Handle(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		in, err := httpx.Decode[struct {
			Kind string `json:"kind"`
		}](w, r)
		if err != nil {
			return 0, nil, err
		}
		res, err := h.svc.Enqueue(r.Context(), authz.MustPrincipal(r.Context()), in.Kind, r.Header.Get("Idempotency-Key"))
		status := http.StatusAccepted
		if res.Existing {
			status = http.StatusOK
		}
		return status, res, err
	}))
	r.Get("/sync-jobs/{id}", httpx.Handle(func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		id, err := httpx.URLParamUUID(r, "id")
		if err != nil {
			return 0, nil, err
		}
		res, err := h.svc.GetJob(r.Context(), authz.MustPrincipal(r.Context()), id)
		return http.StatusOK, res, err
	}))
	r.Post("/sync-jobs/{id}/retry", httpx.Handle(func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		id, err := httpx.URLParamUUID(r, "id")
		if err != nil {
			return 0, nil, err
		}
		res, err := h.svc.Retry(r.Context(), authz.MustPrincipal(r.Context()), id)
		return http.StatusAccepted, res, err
	}))
	r.Get("/external-records", httpx.Handle(func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		entity, err := input.QueryEnum(r, "entity_type", "customer", "invoice", "payment")
		if err != nil {
			return 0, nil, err
		}
		res, err := h.svc.ListExternal(r.Context(), authz.MustPrincipal(r.Context()), entity, input.PageFrom(r))
		return http.StatusOK, map[string]any{"data": res}, err
	}))
}

// Callback handles Intuit's redirect. It needs the session cookie (SameSite=Lax is sent on this
// top-level navigation) and redirects back to the Integrations page with the outcome.
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, h.frontendOrigin+"/login", http.StatusFound)
		return
	}
	if e := q.Get("error"); e != "" {
		http.Redirect(w, r, h.frontendOrigin+"/?integration_error="+url.QueryEscape(e), http.StatusFound)
		return
	}
	orgID, err := h.svc.Callback(r.Context(), u.ID, q.Get("state"), q.Get("code"), q.Get("realmId"))
	target := h.frontendOrigin + "/o/" + orgID.String() + "/integrations"
	if err != nil {
		msg := "connection failed"
		var ae *apperr.Error
		if errors.As(err, &ae) {
			msg = ae.Message
		} else {
			slog.ErrorContext(r.Context(), "quickbooks callback failed", "err", err)
		}
		http.Redirect(w, r, target+"?error="+url.QueryEscape(msg), http.StatusFound)
		return
	}
	http.Redirect(w, r, target+"?connected=1", http.StatusFound)
}

func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		httpx.WriteError(w, r, apperr.BadRequest("could not read body"))
		return
	}
	queued, err := h.svc.Webhook(r.Context(), body, r.Header.Get("intuit-signature"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int{"queued": queued})
}
