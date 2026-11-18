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

	"finopshq/internal/audit"
	"finopshq/internal/auth"
	"finopshq/internal/authz"
	"finopshq/internal/config"
	"finopshq/internal/customers"
	"finopshq/internal/expenses"
	"finopshq/internal/httpx"
	"finopshq/internal/invoices"
	"finopshq/internal/orgs"
	"finopshq/internal/projects"
	"finopshq/internal/qbsync"
	"finopshq/internal/reconcile"
	"finopshq/internal/reports"
)

type Deps struct {
	Config config.Config
	Pool   *pgxpool.Pool
	Sync   *qbsync.Service
}

func New(d Deps) http.Handler {
	if d.Sync == nil {
		svc, _, err := qbsync.Build(d.Config, d.Pool)
		if err != nil {
			panic(err)
		}
		d.Sync = svc
	}
	syncH := qbsync.NewHandler(d.Sync, d.Config.FrontendOrigin)
	authSvc := auth.NewService(d.Pool)
	authH := auth.NewHandler(authSvc, !d.Config.IsDevelopment())
	orgSvc := orgs.NewService(d.Pool, ProvisionOrganization)
	orgH := orgs.NewHandler(orgSvc)
	authH.MeExtras = orgH.MeExtras
	customerH := customers.NewHandler(customers.NewService(d.Pool))
	projectH := projects.NewHandler(projects.NewService(d.Pool))
	invoiceH := invoices.NewHandler(invoices.NewService(d.Pool))
	expenseH := expenses.NewHandler(expenses.NewService(d.Pool))
	reportSvc := reports.NewService(d.Pool)
	reportSvc.SyncActivity = qbsync.RecentJobs
	reconH := reconcile.NewHandler(&reconcile.Service{Pool: d.Pool})
	auditH := audit.NewHandler(d.Pool)
	reportH := reports.NewHandler(reportSvc)

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

		api.Post("/webhooks/quickbooks", syncH.Webhook)

		api.Group(func(sess chi.Router) {
			sess.Use(authH.Middleware)
			sess.Route("/auth", authH.Routes)

			sess.Group(func(authed chi.Router) {
				authed.Use(auth.RequireUser)
				authed.Get("/currencies", orgH.Currencies)
				authed.Get("/integrations/quickbooks/callback", syncH.Callback)
				authed.Route("/orgs", func(or chi.Router) {
					orgH.CollectionRoutes(or)
					or.Route("/{orgID}", func(org chi.Router) {
						org.Use(authz.OrgMiddleware(d.Pool))
						orgH.OrgRoutes(org)
						org.Route("/customers", customerH.Routes)
						org.Route("/projects", projectH.ProjectRoutes)
						org.Route("/budgets", projectH.BudgetRoutes)
						org.Route("/expense-categories", projectH.CategoryRoutes)
						org.Route("/invoices", invoiceH.InvoiceRoutes)
						org.Route("/payments", invoiceH.PaymentRoutes)
						org.Route("/expenses", expenseH.Routes)
						org.Get("/vendors", expenseH.Vendors)
						org.Get("/dashboard", reportH.DashboardRoute)
						org.Route("/reports", reportH.Routes)
						syncH.OrgRoutes(org)
						org.Route("/reconciliation", reconH.Routes)
						org.Get("/audit-logs", auditH.List)
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
