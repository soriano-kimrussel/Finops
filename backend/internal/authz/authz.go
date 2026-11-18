package authz

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"finopshq/internal/apperr"
	"finopshq/internal/auth"
	"finopshq/internal/httpx"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

type Role string

const (
	RoleOwner          Role = "owner"
	RoleAdmin          Role = "admin"
	RoleAccountant     Role = "accountant"
	RoleProjectManager Role = "project_manager"
	RoleViewer         Role = "viewer"
)

func (r Role) Valid() bool {
	_, ok := matrix[r]
	return ok
}

type Permission string

const (
	OrgManage          Permission = "org.manage"
	MembersView        Permission = "members.view"
	MembersManage      Permission = "members.manage"
	CustomersView      Permission = "customers.view"
	CustomersManage    Permission = "customers.manage"
	ProjectsView       Permission = "projects.view"
	ProjectsManage     Permission = "projects.manage"
	BudgetsView        Permission = "budgets.view"
	BudgetsManage      Permission = "budgets.manage"
	InvoicesView       Permission = "invoices.view"
	InvoicesManage     Permission = "invoices.manage"
	PaymentsRecord     Permission = "payments.record"
	ExpensesView       Permission = "expenses.view"
	ExpensesSubmit     Permission = "expenses.submit"
	ExpensesApprove    Permission = "expenses.approve"
	ReportsView        Permission = "reports.view"
	IntegrationsView   Permission = "integrations.view"
	IntegrationsManage Permission = "integrations.manage"
	SyncRun            Permission = "sync.run"
	ReconcileView      Permission = "reconciliation.view"
	ReconcileReview    Permission = "reconciliation.review"
	AuditView          Permission = "audit.view"
)

var all = []Permission{
	OrgManage, MembersView, MembersManage, CustomersView, CustomersManage, ProjectsView, ProjectsManage,
	BudgetsView, BudgetsManage, InvoicesView, InvoicesManage, PaymentsRecord, ExpensesView, ExpensesSubmit,
	ExpensesApprove, ReportsView, IntegrationsView, IntegrationsManage, SyncRun, ReconcileView, ReconcileReview, AuditView,
}

var readOnly = []Permission{MembersView, CustomersView, ProjectsView, BudgetsView, InvoicesView, ExpensesView, ReportsView}

var matrix = map[Role]map[Permission]bool{
	RoleOwner: set(all...),
	RoleAdmin: set(all...),
	RoleAccountant: set(append(readOnly,
		CustomersManage, InvoicesManage, PaymentsRecord, ExpensesSubmit, ExpensesApprove,
		IntegrationsView, SyncRun, ReconcileView, ReconcileReview, AuditView)...),
	RoleProjectManager: set(append(readOnly, ProjectsManage, BudgetsManage, ExpensesSubmit)...),
	RoleViewer:         set(readOnly...),
}

func set(ps ...Permission) map[Permission]bool {
	m := make(map[Permission]bool, len(ps))
	for _, p := range ps {
		m[p] = true
	}
	return m
}

func (r Role) Can(p Permission) bool { return matrix[r][p] }

func (r Role) Permissions() []string {
	out := make([]string, 0, len(matrix[r]))
	for p := range matrix[r] {
		out = append(out, string(p))
	}
	sort.Strings(out)
	return out
}

type Org struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	BaseCurrency string    `json:"base_currency"`
	Timezone     string    `json:"timezone"`
}

// Principal is the authenticated user acting inside one organization.
type Principal struct {
	User auth.User
	Org  Org
	Role Role
}

func (p Principal) Scope() tenant.Scope { return tenant.Scope{OrgID: p.Org.ID, UserID: p.User.ID} }

func (p Principal) Can(perm Permission) bool { return p.Role.Can(perm) }

func (p Principal) Require(perm Permission) error {
	if !p.Can(perm) {
		return apperr.Forbidden("")
	}
	return nil
}

// Location returns the organization's time zone; "today" for due dates is evaluated there.
func (p Principal) Location() *time.Location {
	loc, err := time.LoadLocation(p.Org.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

func (p Principal) Today() time.Time {
	now := time.Now().In(p.Location())
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

type ctxKey struct{}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

func MustPrincipal(ctx context.Context) Principal {
	p, ok := FromContext(ctx)
	if !ok {
		panic("authz: no principal in context; route is missing OrgMiddleware")
	}
	return p
}

// OrgMiddleware resolves {orgID} and the caller's membership. Non-members get 404 so the
// existence of other organizations is not revealed.
func OrgMiddleware(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, ok := auth.UserFromContext(r.Context())
			if !ok {
				httpx.WriteError(w, r, apperr.Unauthorized())
				return
			}
			orgID, err := uuid.Parse(chi.URLParam(r, "orgID"))
			if err != nil {
				httpx.WriteError(w, r, apperr.NotFound("organization"))
				return
			}
			var p Principal
			err = tenant.WithTx(r.Context(), pool, tenant.Scope{OrgID: orgID, UserID: u.ID}, func(tx pgx.Tx) error {
				q := store.New(tx)
				m, err := q.GetMembershipForUser(r.Context(), store.GetMembershipForUserParams{OrganizationID: orgID, UserID: u.ID})
				if err != nil {
					return err
				}
				o, err := q.GetOrganization(r.Context(), orgID)
				if err != nil {
					return err
				}
				p = Principal{User: u, Role: Role(m.Role), Org: Org{ID: o.ID, Name: o.Name, BaseCurrency: o.BaseCurrency, Timezone: o.Timezone}}
				return nil
			})
			if apperr.IsNoRows(err) {
				httpx.WriteError(w, r, apperr.NotFound("organization"))
				return
			}
			if err != nil {
				httpx.WriteError(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		})
	}
}

// Require is route-level permission middleware. Services re-check where rules depend on data.
func Require(perm Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := FromContext(r.Context())
			if !ok || !p.Can(perm) {
				httpx.WriteError(w, r, apperr.Forbidden(""))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
