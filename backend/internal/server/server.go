package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"finopshq/internal/auth"
	"finopshq/internal/authz"
	"finopshq/internal/config"
	"finopshq/internal/httpx"
	"finopshq/internal/orgs"
)

type Deps struct {
	Config config.Config
	Pool   *pgxpool.Pool
}

func New(d Deps) http.Handler {
	authSvc := auth.NewService(d.Pool)
	authH := auth.NewHandler(authSvc, !d.Config.IsDevelopment())
	orgSvc := orgs.NewService(d.Pool, provisionOrganization)
	orgH := orgs.NewHandler(orgSvc)
	authH.MeExtras = orgH.MeExtras

	r := chi.NewRouter()
	r.Use(httpx.Recoverer, httpx.WithRequestID, httpx.Logger, httpx.SecurityHeaders)
	r.Use(httpx.CSRFGuard(d.Config.FrontendOrigin, isWebhook))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := d.Pool.Ping(ctx); err != nil {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "database": "unreachable"})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "database": "ok"})
	})

	r.Route("/api", func(api chi.Router) {
		api.NotFound(func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": map[string]string{"code": "not_found", "message": "no such endpoint"}})
		})

		api.Group(func(sess chi.Router) {
			sess.Use(authH.Middleware)
			sess.Route("/auth", authH.Routes)

			sess.Group(func(authed chi.Router) {
				authed.Use(auth.RequireUser)
				authed.Get("/currencies", orgH.Currencies)
				authed.Route("/orgs", func(or chi.Router) {
					orgH.CollectionRoutes(or)
					or.Route("/{orgID}", func(org chi.Router) {
						org.Use(authz.OrgMiddleware(d.Pool))
						orgH.OrgRoutes(org)
					})
				})
			})
		})
	})

	if d.Config.StaticDir != "" {
		r.NotFound(spaHandler(d.Config.StaticDir))
	}
	return r
}

// Webhooks are called server-to-server and authenticated by signature, not cookies.
func isWebhook(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/api/webhooks/") }

func spaHandler(dir string) http.HandlerFunc {
	files := http.FileServer(http.Dir(dir))
	return func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if info, err := os.Stat(filepath.Join(dir, clean)); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	}
}
