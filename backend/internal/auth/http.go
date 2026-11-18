package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"finopshq/internal/apperr"
	"finopshq/internal/httpx"
)

const CookieName = "finops_session"

type ctxKey struct{}

func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
}

func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

type Handler struct {
	svc          *Service
	secureCookie bool
	// Extra data for GET /me, e.g. organization memberships. Supplied by the orgs package.
	MeExtras func(ctx context.Context, u User) (map[string]any, error)
}

func NewHandler(svc *Service, secureCookie bool) *Handler {
	return &Handler{svc: svc, secureCookie: secureCookie}
}

// Middleware attaches the session user to the context when a valid cookie is present.
func (h *Handler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, err := h.svc.Authenticate(r.Context(), c.Value)
		if errors.Is(err, ErrNoSession) {
			h.clearCookie(w)
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
	})
}

func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFromContext(r.Context()); !ok {
			httpx.WriteError(w, r, apperr.Unauthorized())
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) Routes(r chi.Router) {
	r.Post("/register", h.register)
	r.Post("/login", h.login)
	r.Post("/logout", h.logout)
	r.With(RequireUser).Get("/me", h.me)
}

func clientInfo(r *http.Request) ClientInfo {
	return ClientInfo{IP: httpx.ClientIP(r), UserAgent: r.UserAgent()}
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var in RegisterInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	u, token, err := h.svc.Register(r.Context(), in, clientInfo(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.setCookie(w, token)
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"user": u})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	u, token, err := h.svc.Login(r.Context(), in.Email, in.Password, clientInfo(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.setCookie(w, token)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		if err := h.svc.Logout(r.Context(), c.Value); err != nil {
			slog.ErrorContext(r.Context(), "logout failed", "err", err)
		}
	}
	h.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	body := map[string]any{"user": u}
	if h.MeExtras != nil {
		extra, err := h.MeExtras(r.Context(), u)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		for k, v := range extra {
			body[k] = v
		}
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

func (h *Handler) setCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(SessionAbsoluteTimeout / time.Second),
	})
}

func (h *Handler) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: h.secureCookie,
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}
