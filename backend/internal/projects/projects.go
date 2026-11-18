package projects

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"finopshq/internal/apperr"
	"finopshq/internal/audit"
	"finopshq/internal/authz"
	"finopshq/internal/budget"
	"finopshq/internal/httpx"
	"finopshq/internal/input"
	"finopshq/internal/money"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

var statuses = []string{"planned", "active", "on_hold", "completed", "cancelled"}

type BudgetSummary struct {
	Total       string       `json:"total"`
	Actual      string       `json:"actual"`
	Pending     string       `json:"pending"`
	Remaining   string       `json:"remaining"`
	PercentUsed string       `json:"percent_used"`
	Level       budget.Level `json:"level"`
}

func summarize(a budget.Assessment, currency string) BudgetSummary {
	return BudgetSummary{
		Total: money.Format(a.Budget, currency), Actual: money.Format(a.Actual, currency), Pending: money.Format(a.Pending, currency),
		Remaining: money.Format(a.Remaining, currency), PercentUsed: a.PercentUsed.StringFixed(1), Level: a.Level,
	}
}

type Project struct {
	ID           uuid.UUID     `json:"id"`
	CustomerID   uuid.UUID     `json:"customer_id"`
	CustomerName string        `json:"customer_name"`
	Name         string        `json:"name"`
	Code         *string       `json:"code"`
	Description  *string       `json:"description"`
	Status       string        `json:"status"`
	Currency     string        `json:"currency"`
	StartDate    string        `json:"start_date"`
	EndDate      *string       `json:"end_date"`
	CreatedAt    time.Time     `json:"created_at"`
	Budget       BudgetSummary `json:"budget"`
}

type Input struct {
	CustomerID  string  `json:"customer_id"`
	Name        string  `json:"name"`
	Code        *string `json:"code"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
	Currency    string  `json:"currency"`
	StartDate   string  `json:"start_date"`
	EndDate     *string `json:"end_date"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

type cleanInput struct {
	customerID        uuid.UUID
	name, status, cur string
	code, description *string
	start             time.Time
	end               *time.Time
}

func validate(in Input) (cleanInput, *apperr.Validation) {
	var v apperr.Validation
	c := cleanInput{
		customerID:  input.RequiredUUID(&v, "customer_id", in.CustomerID),
		name:        input.Text(&v, "name", in.Name, 200),
		code:        input.OptionalText(&v, "code", in.Code, 40),
		description: input.OptionalText(&v, "description", in.Description, 2000),
		status:      strings.TrimSpace(in.Status),
		cur:         strings.ToUpper(strings.TrimSpace(in.Currency)),
		start:       input.Date(&v, "start_date", in.StartDate),
		end:         input.OptionalDate(&v, "end_date", in.EndDate),
	}
	if c.status == "" {
		c.status = "active"
	}
	if !contains(statuses, c.status) {
		v.Add("status", "must be one of planned, active, on_hold, completed, cancelled")
	}
	if c.end != nil && !v.Has("start_date") && c.end.Before(c.start) {
		v.Add("end_date", "must be on or after the start date")
	}
	return c, &v
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func mapUniqueErr(err error) error {
	if apperr.IsUniqueViolation(err, "projects_code_key") {
		return (&apperr.Validation{}).With("code", "is already used by another project")
	}
	return err
}

func (s *Service) Create(ctx context.Context, p authz.Principal, in Input) (Project, error) {
	if err := p.Require(authz.ProjectsManage); err != nil {
		return Project{}, err
	}
	c, v := validate(in)
	var out Project
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		var cust store.Customer
		if !v.Has("customer_id") {
			var err error
			cust, err = q.GetCustomer(ctx, c.customerID)
			if apperr.IsNoRows(err) {
				v.Add("customer_id", "does not exist")
			} else if err != nil {
				return err
			} else if cust.Status != "active" {
				v.Add("customer_id", "is inactive")
			}
		}
		if c.cur == "" {
			c.cur = cust.Currency
		}
		if c.cur != "" && !money.Supported(c.cur) {
			v.Add("currency", "is not a supported currency")
		}
		if err := v.Err(); err != nil {
			return err
		}
		row, err := q.CreateProject(ctx, store.CreateProjectParams{
			OrganizationID: p.Org.ID, CustomerID: c.customerID, Name: c.name, Code: c.code, Description: c.description,
			Status: c.status, Currency: c.cur, StartDate: c.start, EndDate: c.end,
		})
		if err != nil {
			return mapUniqueErr(err)
		}
		out = fromRow(row, cust.Name, budget.Assess(decimal.Zero, decimal.Zero, decimal.Zero))
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "project.created", EntityType: "project", EntityID: row.ID, After: out})
	})
	return out, err
}

func (s *Service) Update(ctx context.Context, p authz.Principal, id uuid.UUID, in Input) (Project, error) {
	if err := p.Require(authz.ProjectsManage); err != nil {
		return Project{}, err
	}
	c, v := validate(in)
	var out Project
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetProject(ctx, id)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("project")
		}
		if err != nil {
			return err
		}
		if c.cur != "" && c.cur != cur.Currency {
			v.Add("currency", "cannot be changed after the project is created")
		}
		custName := cur.CustomerName
		if !v.Has("customer_id") && c.customerID != cur.CustomerID {
			cust, err := q.GetCustomer(ctx, c.customerID)
			if apperr.IsNoRows(err) {
				v.Add("customer_id", "does not exist")
			} else if err != nil {
				return err
			}
			custName = cust.Name
		}
		if err := v.Err(); err != nil {
			return err
		}
		row, err := q.UpdateProject(ctx, store.UpdateProjectParams{
			ID: id, CustomerID: c.customerID, Name: c.name, Code: c.code, Description: c.description,
			Status: c.status, StartDate: c.start, EndDate: c.end,
		})
		if err != nil {
			return mapUniqueErr(err)
		}
		before := fromRow(projectOf(cur), cur.CustomerName, budget.Assessment{})
		out = fromRow(row, custName, budget.Assessment{})
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "project.updated", EntityType: "project", EntityID: id,
			Before: withoutBudget(before), After: withoutBudget(out)})
	})
	if err != nil {
		return Project{}, err
	}
	d, err := s.Get(ctx, p, id)
	return d.Project, err
}

func withoutBudget(p Project) map[string]any {
	return map[string]any{"customer_id": p.CustomerID, "name": p.Name, "code": p.Code, "description": p.Description,
		"status": p.Status, "start_date": p.StartDate, "end_date": p.EndDate}
}

func projectOf(r store.GetProjectRow) store.Project {
	return store.Project{ID: r.ID, OrganizationID: r.OrganizationID, CustomerID: r.CustomerID, Name: r.Name, Code: r.Code,
		Description: r.Description, Status: r.Status, Currency: r.Currency, StartDate: r.StartDate, EndDate: r.EndDate,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func fromRow(r store.Project, customerName string, a budget.Assessment) Project {
	return Project{ID: r.ID, CustomerID: r.CustomerID, CustomerName: customerName, Name: r.Name, Code: r.Code,
		Description: r.Description, Status: r.Status, Currency: r.Currency, StartDate: input.FormatDate(r.StartDate),
		EndDate: input.FormatDatePtr(r.EndDate), CreatedAt: r.CreatedAt, Budget: summarize(a, r.Currency)}
}

type ListResult struct {
	Data  []Project `json:"data"`
	Total int64     `json:"total"`
}

func (s *Service) List(ctx context.Context, p authz.Principal, f store.ListProjectsParams) (ListResult, error) {
	if err := p.Require(authz.ProjectsView); err != nil {
		return ListResult{}, err
	}
	var res ListResult
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		rows, err := q.ListProjects(ctx, f)
		if err != nil {
			return err
		}
		res.Total, err = q.CountProjects(ctx, store.CountProjectsParams{Pattern: f.Pattern, Status: f.Status, CustomerID: f.CustomerID})
		if err != nil {
			return err
		}
		res.Data = make([]Project, 0, len(rows))
		for _, r := range rows {
			pr := store.Project{ID: r.ID, CustomerID: r.CustomerID, Name: r.Name, Code: r.Code, Description: r.Description,
				Status: r.Status, Currency: r.Currency, StartDate: r.StartDate, EndDate: r.EndDate, CreatedAt: r.CreatedAt}
			res.Data = append(res.Data, fromRow(pr, r.CustomerName, budget.Assess(r.BudgetTotal, r.ActualTotal, r.PendingTotal)))
		}
		return nil
	})
	return res, err
}

type BudgetLine struct {
	ID           uuid.UUID     `json:"id"`
	ProjectID    uuid.UUID     `json:"project_id"`
	ProjectName  string        `json:"project_name,omitempty"`
	CategoryID   *uuid.UUID    `json:"category_id"`
	CategoryName string        `json:"category_name"`
	Amount       string        `json:"amount"`
	Currency     string        `json:"currency"`
	Notes        *string       `json:"notes"`
	Version      int32         `json:"version"`
	Consumption  BudgetSummary `json:"consumption"`
}

type Billing struct {
	Billed       string `json:"billed"`
	Collected    string `json:"collected"`
	Outstanding  string `json:"outstanding"`
	InvoiceCount int64  `json:"invoice_count"`
}

type Detail struct {
	Project     Project      `json:"project"`
	BudgetLines []BudgetLine `json:"budget_lines"`
	Unbudgeted  struct {
		Approved string `json:"approved"`
		Pending  string `json:"pending"`
	} `json:"unbudgeted"`
	Billing Billing `json:"billing"`
}

func categoryLabel(name *string) string {
	if name == nil {
		return "General"
	}
	return *name
}

func (s *Service) Get(ctx context.Context, p authz.Principal, id uuid.UUID) (Detail, error) {
	if err := p.Require(authz.ProjectsView); err != nil {
		return Detail{}, err
	}
	var d Detail
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetProject(ctx, id)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("project")
		}
		if err != nil {
			return err
		}
		lines, err := q.ListBudgetLines(ctx, id)
		if err != nil {
			return err
		}
		spendRows, err := q.ProjectExpenseTotalsByCategory(ctx, &id)
		if err != nil {
			return err
		}
		bl := make([]budget.Line, 0, len(lines))
		for _, l := range lines {
			bl = append(bl, budget.Line{ID: l.ID, CategoryID: l.CategoryID, Amount: l.Amount})
		}
		sp := make([]budget.Spend, 0, len(spendRows))
		for _, r := range spendRows {
			sp = append(sp, budget.Spend{CategoryID: r.CategoryID, Approved: r.Approved, Pending: r.Pending})
		}
		alloc := budget.Allocate(bl, sp)
		cur := row.Currency
		d.Project = fromRow(projectOf(row), row.CustomerName, budget.Assess(alloc.TotalBudget, alloc.Total.Approved, alloc.Total.Pending))
		d.BudgetLines = make([]BudgetLine, 0, len(lines))
		for _, l := range lines {
			used := alloc.ByLine[l.ID]
			d.BudgetLines = append(d.BudgetLines, BudgetLine{ID: l.ID, ProjectID: l.ProjectID, CategoryID: l.CategoryID,
				CategoryName: categoryLabel(l.CategoryName), Amount: money.Format(l.Amount, cur), Currency: cur, Notes: l.Notes,
				Version: l.Version, Consumption: summarize(budget.Assess(l.Amount, used.Approved, used.Pending), cur)})
		}
		d.Unbudgeted.Approved = money.Format(alloc.Unbudgeted.Approved, cur)
		d.Unbudgeted.Pending = money.Format(alloc.Unbudgeted.Pending, cur)

		b, err := q.ProjectBilling(ctx, &id)
		if err != nil {
			return err
		}
		d.Billing = Billing{Billed: money.Format(b.Billed, cur), Collected: money.Format(b.Collected, cur),
			Outstanding: money.Format(b.Billed.Sub(b.Collected), cur), InvoiceCount: b.InvoiceCount}
		return nil
	})
	return d, err
}

// Budgets

type BudgetInput struct {
	ProjectID  string  `json:"project_id"`
	CategoryID *string `json:"category_id"`
	Amount     string  `json:"amount"`
	Notes      *string `json:"notes"`
	Version    *int32  `json:"version"`
}

func budgetSnapshot(b store.Budget) map[string]any {
	return map[string]any{"project_id": b.ProjectID, "category_id": b.CategoryID, "amount": money.Format(b.Amount, b.Currency),
		"currency": b.Currency, "notes": b.Notes}
}

func (s *Service) CreateBudget(ctx context.Context, p authz.Principal, in BudgetInput) (uuid.UUID, error) {
	if err := p.Require(authz.BudgetsManage); err != nil {
		return uuid.Nil, err
	}
	var v apperr.Validation
	projectID := input.RequiredUUID(&v, "project_id", in.ProjectID)
	categoryID := input.OptionalUUID(&v, "category_id", in.CategoryID)
	notes := input.OptionalText(&v, "notes", in.Notes, 500)
	var id uuid.UUID
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		var proj store.GetProjectRow
		if !v.Has("project_id") {
			var err error
			proj, err = q.GetProject(ctx, projectID)
			if apperr.IsNoRows(err) {
				v.Add("project_id", "does not exist")
			} else if err != nil {
				return err
			}
		}
		if categoryID != nil {
			if _, err := q.GetExpenseCategory(ctx, *categoryID); apperr.IsNoRows(err) {
				v.Add("category_id", "does not exist")
			} else if err != nil {
				return err
			}
		}
		var amount decimal.Decimal
		if !v.Has("project_id") {
			var err error
			if amount, err = money.ParseAmount(in.Amount, proj.Currency); err != nil {
				v.Add("amount", err.Error())
			}
		}
		if err := v.Err(); err != nil {
			return err
		}
		b, err := q.CreateBudget(ctx, store.CreateBudgetParams{OrganizationID: p.Org.ID, ProjectID: projectID, CategoryID: categoryID,
			Currency: proj.Currency, Amount: amount, Notes: notes, CreatedBy: &p.User.ID})
		if apperr.IsUniqueViolation(err, "budgets_project_category_key") {
			return apperr.Conflict("budget_exists", "this project already has a budget line for that category; edit it instead")
		}
		if err != nil {
			return err
		}
		id = b.ID
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "budget.created", EntityType: "budget", EntityID: b.ID, After: budgetSnapshot(b)})
	})
	return id, err
}

func (s *Service) UpdateBudget(ctx context.Context, p authz.Principal, id uuid.UUID, in BudgetInput) error {
	if err := p.Require(authz.BudgetsManage); err != nil {
		return err
	}
	return tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetBudgetForUpdate(ctx, id)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("budget")
		}
		if err != nil {
			return err
		}
		if in.Version != nil && *in.Version != cur.Version {
			return apperr.Conflict("stale_version", "this budget was changed by someone else; reload and try again")
		}
		var v apperr.Validation
		amount, err := money.ParseAmount(in.Amount, cur.Currency)
		if err != nil {
			v.Add("amount", err.Error())
		}
		notes := input.OptionalText(&v, "notes", in.Notes, 500)
		if err := v.Err(); err != nil {
			return err
		}
		b, err := q.UpdateBudget(ctx, store.UpdateBudgetParams{ID: id, Amount: amount, Notes: notes})
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "budget.updated", EntityType: "budget", EntityID: id,
			Before: budgetSnapshot(cur), After: budgetSnapshot(b)})
	})
}

func (s *Service) DeleteBudget(ctx context.Context, p authz.Principal, id uuid.UUID) error {
	if err := p.Require(authz.BudgetsManage); err != nil {
		return err
	}
	return tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetBudgetForUpdate(ctx, id)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("budget")
		}
		if err != nil {
			return err
		}
		if err := q.DeleteBudget(ctx, id); err != nil {
			return err
		}
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "budget.deleted", EntityType: "budget", EntityID: id, Before: budgetSnapshot(cur)})
	})
}

type BudgetOverview struct {
	ProjectID     uuid.UUID     `json:"project_id"`
	ProjectName   string        `json:"project_name"`
	ProjectStatus string        `json:"project_status"`
	Currency      string        `json:"currency"`
	Lines         []BudgetLine  `json:"lines"`
	Totals        BudgetSummary `json:"totals"`
}

// ListBudgets returns every budgeted project with its lines and consumption.
func (s *Service) ListBudgets(ctx context.Context, p authz.Principal, projectID *uuid.UUID) ([]BudgetOverview, error) {
	if err := p.Require(authz.BudgetsView); err != nil {
		return nil, err
	}
	var out []BudgetOverview
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		rows, err := q.ListAllBudgets(ctx, projectID)
		if err != nil {
			return err
		}
		spendRows, err := q.ExpenseTotalsByProjectCategory(ctx, projectID)
		if err != nil {
			return err
		}
		spendByProject := map[uuid.UUID][]budget.Spend{}
		for _, r := range spendRows {
			spendByProject[r.ProjectID] = append(spendByProject[r.ProjectID], budget.Spend{CategoryID: r.CategoryID, Approved: r.Approved, Pending: r.Pending})
		}
		index := map[uuid.UUID]int{}
		lines := map[uuid.UUID][]budget.Line{}
		for _, r := range rows {
			if _, ok := index[r.ProjectID]; !ok {
				index[r.ProjectID] = len(out)
				out = append(out, BudgetOverview{ProjectID: r.ProjectID, ProjectName: r.ProjectName, ProjectStatus: r.ProjectStatus, Currency: r.Currency})
			}
			lines[r.ProjectID] = append(lines[r.ProjectID], budget.Line{ID: r.ID, CategoryID: r.CategoryID, Amount: r.Amount})
		}
		for pid, i := range index {
			alloc := budget.Allocate(lines[pid], spendByProject[pid])
			out[i].Totals = summarize(budget.Assess(alloc.TotalBudget, alloc.Total.Approved, alloc.Total.Pending), out[i].Currency)
			for _, r := range rows {
				if r.ProjectID != pid {
					continue
				}
				used := alloc.ByLine[r.ID]
				out[i].Lines = append(out[i].Lines, BudgetLine{ID: r.ID, ProjectID: pid, ProjectName: r.ProjectName, CategoryID: r.CategoryID,
					CategoryName: categoryLabel(r.CategoryName), Amount: money.Format(r.Amount, r.Currency), Currency: r.Currency,
					Notes: r.Notes, Version: r.Version, Consumption: summarize(budget.Assess(r.Amount, used.Approved, used.Pending), r.Currency)})
			}
		}
		return nil
	})
	if out == nil {
		out = []BudgetOverview{}
	}
	return out, err
}

// Expense categories

type Category struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	IsArchived bool      `json:"is_archived"`
}

func (s *Service) Categories(ctx context.Context, p authz.Principal) ([]Category, error) {
	var out []Category
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListExpenseCategories(ctx, store.ListExpenseCategoriesParams{OrganizationID: p.Org.ID})
		if err != nil {
			return err
		}
		out = make([]Category, 0, len(rows))
		for _, r := range rows {
			out = append(out, Category{ID: r.ID, Name: r.Name, IsArchived: r.IsArchived})
		}
		return nil
	})
	return out, err
}

func (s *Service) CreateCategory(ctx context.Context, p authz.Principal, name string) (Category, error) {
	if !p.Can(authz.ExpensesApprove) && !p.Can(authz.BudgetsManage) {
		return Category{}, apperr.Forbidden("")
	}
	var v apperr.Validation
	name = input.Text(&v, "name", name, 100)
	if err := v.Err(); err != nil {
		return Category{}, err
	}
	var out Category
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		c, err := q.CreateExpenseCategory(ctx, store.CreateExpenseCategoryParams{OrganizationID: p.Org.ID, Name: name})
		if apperr.IsUniqueViolation(err, "expense_categories_org_name_key") {
			return (&apperr.Validation{}).With("name", "already exists")
		}
		if err != nil {
			return err
		}
		out = Category{ID: c.ID, Name: c.Name}
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "expense_category.created", EntityType: "expense_category", EntityID: c.ID, After: out})
	})
	return out, err
}

// HTTP

type Handler struct{ svc *Service }

func NewHandler(s *Service) *Handler { return &Handler{svc: s} }

func (h *Handler) ProjectRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(h.list))
	r.Post("/", httpx.Handle(h.create))
	r.Get("/{id}", httpx.Handle(h.get))
	r.Patch("/{id}", httpx.Handle(h.update))
}

func (h *Handler) BudgetRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		pid, err := input.QueryUUID(r, "project_id")
		if err != nil {
			return 0, nil, err
		}
		res, err := h.svc.ListBudgets(r.Context(), authz.MustPrincipal(r.Context()), pid)
		return http.StatusOK, map[string]any{"data": res}, err
	}))
	r.Post("/", httpx.Handle(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		in, err := httpx.Decode[BudgetInput](w, r)
		if err != nil {
			return 0, nil, err
		}
		id, err := h.svc.CreateBudget(r.Context(), authz.MustPrincipal(r.Context()), in)
		return http.StatusCreated, map[string]any{"id": id}, err
	}))
	r.Patch("/{id}", httpx.Handle(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		id, err := httpx.URLParamUUID(r, "id")
		if err != nil {
			return 0, nil, err
		}
		in, err := httpx.Decode[BudgetInput](w, r)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusNoContent, nil, h.svc.UpdateBudget(r.Context(), authz.MustPrincipal(r.Context()), id, in)
	}))
	r.Delete("/{id}", httpx.Handle(func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		id, err := httpx.URLParamUUID(r, "id")
		if err != nil {
			return 0, nil, err
		}
		return http.StatusNoContent, nil, h.svc.DeleteBudget(r.Context(), authz.MustPrincipal(r.Context()), id)
	}))
}

func (h *Handler) CategoryRoutes(r chi.Router) {
	r.Get("/", httpx.Handle(func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		cs, err := h.svc.Categories(r.Context(), authz.MustPrincipal(r.Context()))
		return http.StatusOK, map[string]any{"data": cs}, err
	}))
	r.Post("/", httpx.Handle(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		in, err := httpx.Decode[struct {
			Name string `json:"name"`
		}](w, r)
		if err != nil {
			return 0, nil, err
		}
		c, err := h.svc.CreateCategory(r.Context(), authz.MustPrincipal(r.Context()), in.Name)
		return http.StatusCreated, map[string]any{"category": c}, err
	}))
}

func (h *Handler) list(_ http.ResponseWriter, r *http.Request) (int, any, error) {
	status, err := input.QueryEnum(r, "status", statuses...)
	if err != nil {
		return 0, nil, err
	}
	cust, err := input.QueryUUID(r, "customer_id")
	if err != nil {
		return 0, nil, err
	}
	page := input.PageFrom(r)
	res, err := h.svc.List(r.Context(), authz.MustPrincipal(r.Context()), store.ListProjectsParams{
		Pattern: input.Pattern(r.URL.Query().Get("q")), Status: status, CustomerID: cust, RowLimit: page.Limit, RowOffset: page.Offset,
	})
	return http.StatusOK, res, err
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) (int, any, error) {
	in, err := httpx.Decode[Input](w, r)
	if err != nil {
		return 0, nil, err
	}
	p, err := h.svc.Create(r.Context(), authz.MustPrincipal(r.Context()), in)
	return http.StatusCreated, map[string]any{"project": p}, err
}

func (h *Handler) get(_ http.ResponseWriter, r *http.Request) (int, any, error) {
	id, err := httpx.URLParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	d, err := h.svc.Get(r.Context(), authz.MustPrincipal(r.Context()), id)
	return http.StatusOK, d, err
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) (int, any, error) {
	id, err := httpx.URLParamUUID(r, "id")
	if err != nil {
		return 0, nil, err
	}
	in, err := httpx.Decode[Input](w, r)
	if err != nil {
		return 0, nil, err
	}
	p, err := h.svc.Update(r.Context(), authz.MustPrincipal(r.Context()), id, in)
	return http.StatusOK, map[string]any{"project": p}, err
}
