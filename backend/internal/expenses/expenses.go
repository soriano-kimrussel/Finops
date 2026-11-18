package expenses

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"finopshq/internal/apperr"
	"finopshq/internal/audit"
	"finopshq/internal/authz"
	"finopshq/internal/httpx"
	"finopshq/internal/input"
	"finopshq/internal/ledger"
	"finopshq/internal/money"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

type Expense struct {
	ID              uuid.UUID  `json:"id"`
	VendorName      string     `json:"vendor_name"`
	ExpenseDate     string     `json:"expense_date"`
	Amount          string     `json:"amount"`
	Currency        string     `json:"currency"`
	Description     *string    `json:"description"`
	ApprovalStatus  string     `json:"approval_status"`
	CategoryID      uuid.UUID  `json:"category_id"`
	CategoryName    string     `json:"category_name"`
	ProjectID       *uuid.UUID `json:"project_id"`
	ProjectName     *string    `json:"project_name"`
	SubmittedBy     *uuid.UUID `json:"submitted_by"`
	SubmittedByName *string    `json:"submitted_by_name"`
	ReviewedByName  *string    `json:"reviewed_by_name,omitempty"`
	ReviewedAt      *time.Time `json:"reviewed_at,omitempty"`
	ReviewNote      *string    `json:"review_note,omitempty"`
	Version         int32      `json:"version,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type Input struct {
	CategoryID  string  `json:"category_id"`
	ProjectID   *string `json:"project_id"`
	VendorName  string  `json:"vendor_name"`
	ExpenseDate string  `json:"expense_date"`
	Amount      string  `json:"amount"`
	Currency    string  `json:"currency"`
	Description *string `json:"description"`
	Version     *int32  `json:"version"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) validate(ctx context.Context, q *store.Queries, p authz.Principal, in Input) (store.CreateExpenseParams, error) {
	var v apperr.Validation
	catID := input.RequiredUUID(&v, "category_id", in.CategoryID)
	projID := input.OptionalUUID(&v, "project_id", in.ProjectID)
	vendor := input.Text(&v, "vendor_name", in.VendorName, 200)
	date := input.Date(&v, "expense_date", in.ExpenseDate)
	if !v.Has("expense_date") && date.After(p.Today()) {
		v.Add("expense_date", "cannot be in the future")
	}
	desc := input.OptionalText(&v, "description", in.Description, 1000)
	cur := strings.ToUpper(strings.TrimSpace(in.Currency))

	if !v.Has("category_id") {
		c, err := q.GetExpenseCategory(ctx, catID)
		if apperr.IsNoRows(err) {
			v.Add("category_id", "does not exist")
		} else if err != nil {
			return store.CreateExpenseParams{}, err
		} else if c.IsArchived {
			v.Add("category_id", "is archived")
		}
	}
	if projID != nil {
		pr, err := q.GetProject(ctx, *projID)
		if apperr.IsNoRows(err) {
			v.Add("project_id", "does not exist")
		} else if err != nil {
			return store.CreateExpenseParams{}, err
		} else {
			if cur == "" {
				cur = pr.Currency
			}
			if cur != pr.Currency {
				v.Add("currency", fmt.Sprintf("must match the project currency (%s)", pr.Currency))
			}
		}
	}
	if cur == "" {
		cur = p.Org.BaseCurrency
	}
	if !money.Supported(cur) {
		v.Add("currency", "is not a supported currency")
	}
	amount, err := money.ParseAmount(in.Amount, cur)
	if err != nil {
		v.Add("amount", err.Error())
	}
	if err := v.Err(); err != nil {
		return store.CreateExpenseParams{}, err
	}
	// Vendors are matched case-insensitively by name and created on first use.
	vendorID, err := q.UpsertVendor(ctx, store.UpsertVendorParams{OrganizationID: p.Org.ID, Name: vendor})
	if err != nil {
		return store.CreateExpenseParams{}, err
	}
	return store.CreateExpenseParams{OrganizationID: p.Org.ID, CategoryID: catID, ProjectID: projID, VendorID: vendorID,
		ExpenseDate: date, Amount: amount, Currency: cur, Description: desc, SubmittedBy: &p.User.ID}, nil
}

func snapshot(ctx context.Context, q *store.Queries, e store.Expense) map[string]any {
	vendor := ""
	if v, err := q.GetVendor(ctx, e.VendorID); err == nil {
		vendor = v.Name
	}
	return map[string]any{"vendor_name": vendor, "vendor_id": e.VendorID, "expense_date": input.FormatDate(e.ExpenseDate),
		"amount": money.Format(e.Amount, e.Currency), "currency": e.Currency, "category_id": e.CategoryID,
		"project_id": e.ProjectID, "description": e.Description, "approval_status": e.ApprovalStatus}
}

func (s *Service) Create(ctx context.Context, p authz.Principal, in Input) (Expense, error) {
	if err := p.Require(authz.ExpensesSubmit); err != nil {
		return Expense{}, err
	}
	var out Expense
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		params, err := s.validate(ctx, q, p, in)
		if err != nil {
			return err
		}
		e, err := q.CreateExpense(ctx, params)
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "expense.created", EntityType: "expense", EntityID: e.ID, After: snapshot(ctx, q, e)}); err != nil {
			return err
		}
		out, err = load(ctx, q, e.ID)
		return err
	})
	return out, err
}

func canModify(p authz.Principal, e store.Expense) error {
	if e.ApprovalStatus != "pending" {
		return apperr.Unprocessable("expense_reviewed", "only pending expenses can be changed")
	}
	if e.SubmittedBy != nil && *e.SubmittedBy == p.User.ID {
		return nil
	}
	if p.Can(authz.ExpensesApprove) {
		return nil
	}
	return apperr.Forbidden("you can only change expenses you submitted")
}

func (s *Service) Update(ctx context.Context, p authz.Principal, id uuid.UUID, in Input) (Expense, error) {
	if err := p.Require(authz.ExpensesSubmit); err != nil {
		return Expense{}, err
	}
	var out Expense
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetExpenseForUpdate(ctx, id)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("expense")
		}
		if err != nil {
			return err
		}
		if err := canModify(p, cur); err != nil {
			return err
		}
		if in.Version != nil && *in.Version != cur.Version {
			return apperr.Conflict("stale_version", "this expense was changed by someone else; reload and try again")
		}
		before := snapshot(ctx, q, cur)
		params, err := s.validate(ctx, q, p, in)
		if err != nil {
			return err
		}
		e, err := q.UpdateExpense(ctx, store.UpdateExpenseParams{ID: id, CategoryID: params.CategoryID, ProjectID: params.ProjectID,
			VendorID: params.VendorID, ExpenseDate: params.ExpenseDate, Amount: params.Amount, Currency: params.Currency, Description: params.Description})
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "expense.updated", EntityType: "expense", EntityID: id,
			Before: before, After: snapshot(ctx, q, e)}); err != nil {
			return err
		}
		out, err = load(ctx, q, id)
		return err
	})
	return out, err
}

func (s *Service) Delete(ctx context.Context, p authz.Principal, id uuid.UUID) error {
	if err := p.Require(authz.ExpensesSubmit); err != nil {
		return err
	}
	return tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetExpenseForUpdate(ctx, id)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("expense")
		}
		if err != nil {
			return err
		}
		if err := canModify(p, cur); err != nil {
			return err
		}
		if err := q.DeleteExpense(ctx, id); err != nil {
			return err
		}
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "expense.deleted", EntityType: "expense", EntityID: id, Before: snapshot(ctx, q, cur)})
	})
}

// Review approves or rejects a pending expense. Approval posts the cash outflow to the ledger.
// Reviewers cannot approve their own submissions unless they are an owner.
func (s *Service) Review(ctx context.Context, p authz.Principal, id uuid.UUID, approve bool, note *string) (Expense, error) {
	if err := p.Require(authz.ExpensesApprove); err != nil {
		return Expense{}, err
	}
	var v apperr.Validation
	note = input.OptionalText(&v, "note", note, 1000)
	if !approve && note == nil {
		v.Add("note", "explain why the expense is rejected")
	}
	if err := v.Err(); err != nil {
		return Expense{}, err
	}
	var out Expense
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetExpenseForUpdate(ctx, id)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("expense")
		}
		if err != nil {
			return err
		}
		if cur.ApprovalStatus != "pending" {
			return apperr.Unprocessable("expense_reviewed", "this expense has already been reviewed")
		}
		if cur.SubmittedBy != nil && *cur.SubmittedBy == p.User.ID && p.Role != authz.RoleOwner {
			return apperr.Forbidden("you cannot review an expense you submitted")
		}
		status := "rejected"
		if approve {
			status = "approved"
		}
		e, err := q.ReviewExpense(ctx, store.ReviewExpenseParams{ID: id, ApprovalStatus: status, ReviewedBy: &p.User.ID, ReviewNote: note})
		if err != nil {
			return err
		}
		if approve {
			if err := ledger.Post(ctx, q, ledger.Entry{OrgID: p.Org.ID, OccurredOn: e.ExpenseDate, Direction: "out", Amount: e.Amount,
				Currency: e.Currency, Description: "Expense: " + vendorName(ctx, q, e.VendorID), SourceType: "expense", SourceID: e.ID}); err != nil {
				return err
			}
		}
		if err := audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "expense." + status, EntityType: "expense", EntityID: id,
			Before: map[string]any{"approval_status": "pending"}, After: map[string]any{"approval_status": status, "note": note,
				"amount": money.Format(e.Amount, e.Currency), "currency": e.Currency}}); err != nil {
			return err
		}
		out, err = load(ctx, q, id)
		return err
	})
	return out, err
}

func load(ctx context.Context, q *store.Queries, id uuid.UUID) (Expense, error) {
	r, err := q.GetExpense(ctx, id)
	if apperr.IsNoRows(err) {
		return Expense{}, apperr.NotFound("expense")
	}
	if err != nil {
		return Expense{}, err
	}
	return Expense{ID: r.ID, VendorName: r.VendorName, ExpenseDate: input.FormatDate(r.ExpenseDate), Amount: money.Format(r.Amount, r.Currency),
		Currency: r.Currency, Description: r.Description, ApprovalStatus: r.ApprovalStatus, CategoryID: r.CategoryID, CategoryName: r.CategoryName,
		ProjectID: r.ProjectID, ProjectName: r.ProjectName, SubmittedBy: r.SubmittedBy, SubmittedByName: r.SubmittedByName,
		ReviewedByName: r.ReviewedByName, ReviewedAt: r.ReviewedAt, ReviewNote: r.ReviewNote, Version: r.Version, CreatedAt: r.CreatedAt}, nil
}

func (s *Service) Get(ctx context.Context, p authz.Principal, id uuid.UUID) (Expense, error) {
	if err := p.Require(authz.ExpensesView); err != nil {
		return Expense{}, err
	}
	var out Expense
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		var err error
		out, err = load(ctx, store.New(tx), id)
		return err
	})
	return out, err
}

type ListResult struct {
	Data        []Expense `json:"data"`
	Total       int64     `json:"total"`
	TotalAmount string    `json:"total_amount"`
	Currency    string    `json:"currency"`
}

func (s *Service) List(ctx context.Context, p authz.Principal, f store.ListExpensesParams) (ListResult, error) {
	if err := p.Require(authz.ExpensesView); err != nil {
		return ListResult{}, err
	}
	res := ListResult{Currency: p.Org.BaseCurrency}
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		rows, err := q.ListExpenses(ctx, f)
		if err != nil {
			return err
		}
		c, err := q.CountExpenses(ctx, store.CountExpensesParams{Currency: p.Org.BaseCurrency, Pattern: f.Pattern, Status: f.Status,
			CategoryID: f.CategoryID, ProjectID: f.ProjectID, From: f.From, To: f.To})
		if err != nil {
			return err
		}
		res.Total, res.TotalAmount = c.Total, money.Format(c.AmountSum, p.Org.BaseCurrency)
		res.Data = make([]Expense, 0, len(rows))
		for _, r := range rows {
			res.Data = append(res.Data, Expense{ID: r.ID, VendorName: r.VendorName, ExpenseDate: input.FormatDate(r.ExpenseDate),
				Amount: money.Format(r.Amount, r.Currency), Currency: r.Currency, Description: r.Description, ApprovalStatus: r.ApprovalStatus,
				CategoryID: r.CategoryID, CategoryName: r.CategoryName, ProjectID: r.ProjectID, ProjectName: r.ProjectName,
				SubmittedBy: r.SubmittedBy, SubmittedByName: r.SubmittedByName, CreatedAt: r.CreatedAt})
		}
		return nil
	})
	return res, err
}

type Handler struct{ svc *Service }

func NewHandler(s *Service) *Handler { return &Handler{svc: s} }

func (h *Handler) Routes(r chi.Router) {
	r.Get("/", httpx.Handle(h.list))
	r.Post("/", httpx.Handle(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		in, err := httpx.Decode[Input](w, r)
		if err != nil {
			return 0, nil, err
		}
		e, err := h.svc.Create(r.Context(), authz.MustPrincipal(r.Context()), in)
		return http.StatusCreated, map[string]any{"expense": e}, err
	}))
	r.Get("/{id}", httpx.Handle(func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		id, err := httpx.URLParamUUID(r, "id")
		if err != nil {
			return 0, nil, err
		}
		e, err := h.svc.Get(r.Context(), authz.MustPrincipal(r.Context()), id)
		return http.StatusOK, map[string]any{"expense": e}, err
	}))
	r.Patch("/{id}", httpx.Handle(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		id, err := httpx.URLParamUUID(r, "id")
		if err != nil {
			return 0, nil, err
		}
		in, err := httpx.Decode[Input](w, r)
		if err != nil {
			return 0, nil, err
		}
		e, err := h.svc.Update(r.Context(), authz.MustPrincipal(r.Context()), id, in)
		return http.StatusOK, map[string]any{"expense": e}, err
	}))
	r.Delete("/{id}", httpx.Handle(func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		id, err := httpx.URLParamUUID(r, "id")
		if err != nil {
			return 0, nil, err
		}
		return http.StatusNoContent, nil, h.svc.Delete(r.Context(), authz.MustPrincipal(r.Context()), id)
	}))
	review := func(approve bool) http.HandlerFunc {
		return httpx.Handle(func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			id, err := httpx.URLParamUUID(r, "id")
			if err != nil {
				return 0, nil, err
			}
			var in struct {
				Note *string `json:"note"`
			}
			if r.ContentLength != 0 {
				if err := httpx.DecodeJSON(w, r, &in); err != nil {
					return 0, nil, err
				}
			}
			e, err := h.svc.Review(r.Context(), authz.MustPrincipal(r.Context()), id, approve, in.Note)
			return http.StatusOK, map[string]any{"expense": e}, err
		})
	}
	r.Post("/{id}/approve", review(true))
	r.Post("/{id}/reject", review(false))
}

type Vendor struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	ExpenseCount int64     `json:"expense_count"`
}

func (h *Handler) Vendors(w http.ResponseWriter, r *http.Request) {
	httpx.Handle(func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		p := authz.MustPrincipal(r.Context())
		if err := p.Require(authz.ExpensesView); err != nil {
			return 0, nil, err
		}
		out := []Vendor{}
		err := tenant.WithTx(r.Context(), h.svc.pool, p.Scope(), func(tx pgx.Tx) error {
			rows, err := store.New(tx).ListVendors(r.Context())
			for _, v := range rows {
				out = append(out, Vendor{ID: v.ID, Name: v.Name, ExpenseCount: v.ExpenseCount})
			}
			return err
		})
		return http.StatusOK, map[string]any{"data": out}, err
	})(w, r)
}

func (h *Handler) list(_ http.ResponseWriter, r *http.Request) (int, any, error) {
	status, err := input.QueryEnum(r, "status", "pending", "approved", "rejected")
	if err != nil {
		return 0, nil, err
	}
	cat, err := input.QueryUUID(r, "category_id")
	if err != nil {
		return 0, nil, err
	}
	proj, err := input.QueryUUID(r, "project_id")
	if err != nil {
		return 0, nil, err
	}
	from, to, err := input.DateRange(r)
	if err != nil {
		return 0, nil, err
	}
	page := input.PageFrom(r)
	res, err := h.svc.List(r.Context(), authz.MustPrincipal(r.Context()), store.ListExpensesParams{
		Pattern: input.Pattern(r.URL.Query().Get("q")), Status: status, CategoryID: cat, ProjectID: proj, From: from, To: to,
		RowLimit: page.Limit, RowOffset: page.Offset,
	})
	return http.StatusOK, res, err
}

func vendorName(ctx context.Context, q *store.Queries, id uuid.UUID) string {
	if v, err := q.GetVendor(ctx, id); err == nil {
		return v.Name
	}
	return "vendor"
}
