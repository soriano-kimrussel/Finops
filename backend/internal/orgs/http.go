package orgs

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"finopshq/internal/auth"
	"finopshq/internal/authz"
	"finopshq/internal/httpx"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// MeExtras adds the caller's organizations to GET /api/auth/me.
func (h *Handler) MeExtras(ctx context.Context, u auth.User) (map[string]any, error) {
	ms, err := h.svc.ListForUser(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"memberships": ms}, nil
}

func (h *Handler) CollectionRoutes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
}

func (h *Handler) OrgRoutes(r chi.Router) {
	r.Get("/", h.get)
	r.Patch("/", h.update)
	r.Get("/members", h.listMembers)
	r.Post("/members", h.addMember)
	r.Patch("/members/{memberID}", h.changeRole)
	r.Delete("/members/{memberID}", h.removeMember)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFromContext(r.Context())
	ms, err := h.svc.ListForUser(r.Context(), u.ID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": ms})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFromContext(r.Context())
	var in CreateInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	org, err := h.svc.Create(r.Context(), u, in)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"organization": org, "role": authz.RoleOwner})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p := authz.MustPrincipal(r.Context())
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"organization": p.Org, "role": p.Role, "permissions": p.Role.Permissions(),
	})
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	p := authz.MustPrincipal(r.Context())
	var in UpdateInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	org, err := h.svc.Update(r.Context(), p, in)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"organization": org})
}

func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) {
	ms, err := h.svc.ListMembers(r.Context(), authz.MustPrincipal(r.Context()))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": ms})
}

func (h *Handler) addMember(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	m, err := h.svc.AddMember(r.Context(), authz.MustPrincipal(r.Context()), in.Email, authz.Role(in.Role))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"member": m})
}

func (h *Handler) changeRole(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.URLParamUUID(r, "memberID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	m, err := h.svc.ChangeRole(r.Context(), authz.MustPrincipal(r.Context()), id, authz.Role(in.Role))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"member": m})
}

func (h *Handler) removeMember(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.URLParamUUID(r, "memberID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.svc.RemoveMember(r.Context(), authz.MustPrincipal(r.Context()), id); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Currencies(w http.ResponseWriter, r *http.Request) {
	cs, err := h.svc.Currencies(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	type currency struct {
		Code       string `json:"code"`
		Name       string `json:"name"`
		MinorUnits int16  `json:"minor_units"`
	}
	out := make([]currency, 0, len(cs))
	for _, c := range cs {
		out = append(out, currency{Code: c.Code, Name: c.Name, MinorUnits: c.MinorUnits})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}
