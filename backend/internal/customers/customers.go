package customers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"finopshq/internal/apperr"
	"finopshq/internal/audit"
	"finopshq/internal/auth"
	"finopshq/internal/authz"
	"finopshq/internal/httpx"
	"finopshq/internal/input"
	"finopshq/internal/money"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

type Customer struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	ContactName  *string   `json:"contact_name"`
	Email        *string   `json:"email"`
	Phone        *string   `json:"phone"`
	AddressLine1 *string   `json:"address_line1"`
	AddressLine2 *string   `json:"address_line2"`
	City         *string   `json:"city"`
	Region       *string   `json:"region"`
	PostalCode   *string   `json:"postal_code"`
	Country      *string   `json:"country"`
	Currency     string    `json:"currency"`
	Status       string    `json:"status"`
	Notes        *string   `json:"notes"`
	Source       string    `json:"source"`
	ExternalID   *string   `json:"external_id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Outstanding  *string   `json:"outstanding,omitempty"`
	InvoiceCount *int64    `json:"invoice_count,omitempty"`
}

func FromStore(c store.Customer) Customer {
	return Customer{
		ID: c.ID, Name: c.Name, ContactName: c.ContactName, Email: c.Email, Phone: c.Phone,
		AddressLine1: c.AddressLine1, AddressLine2: c.AddressLine2, City: c.City, Region: c.Region,
		PostalCode: c.PostalCode, Country: c.Country, Currency: c.Currency, Status: c.Status, Notes: c.Notes,
		Source: c.Source, ExternalID: c.ExternalID, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

type Input struct {
	Name         string  `json:"name"`
	ContactName  *string `json:"contact_name"`
	Email        *string `json:"email"`
	Phone        *string `json:"phone"`
	AddressLine1 *string `json:"address_line1"`
	AddressLine2 *string `json:"address_line2"`
	City         *string `json:"city"`
	Region       *string `json:"region"`
	PostalCode   *string `json:"postal_code"`
	Country      *string `json:"country"`
	Currency     string  `json:"currency"`
	Status       string  `json:"status"`
	Notes        *string `json:"notes"`
}

type clean struct {
	Name, Currency, Status                                                            string
	ContactName, Email, Phone, Line1, Line2, City, Region, PostalCode, Country, Notes *string
}

func validate(in Input, defaultCurrency string) (clean, error) {
	var v apperr.Validation
	c := clean{
		Name:        input.Text(&v, "name", in.Name, 200),
		ContactName: input.OptionalText(&v, "contact_name", in.ContactName, 200),
		Email:       input.OptionalText(&v, "email", in.Email, 254),
		Phone:       input.OptionalText(&v, "phone", in.Phone, 50),
		Line1:       input.OptionalText(&v, "address_line1", in.AddressLine1, 200),
		Line2:       input.OptionalText(&v, "address_line2", in.AddressLine2, 200),
		City:        input.OptionalText(&v, "city", in.City, 100),
		Region:      input.OptionalText(&v, "region", in.Region, 100),
		PostalCode:  input.OptionalText(&v, "postal_code", in.PostalCode, 20),
		Country:     input.OptionalText(&v, "country", in.Country, 100),
		Notes:       input.OptionalText(&v, "notes", in.Notes, 2000),
		Currency:    strings.ToUpper(strings.TrimSpace(in.Currency)),
		Status:      strings.TrimSpace(in.Status),
	}
	if c.Email != nil {
		e := auth.NormalizeEmail(*c.Email)
		c.Email = &e
		if !auth.ValidEmail(e) {
			v.Add("email", "must be a valid email address")
		}
	}
	if c.Currency == "" {
		c.Currency = defaultCurrency
	}
	if !money.Supported(c.Currency) {
		v.Add("currency", "is not a supported currency")
	}
	if c.Status == "" {
		c.Status = "active"
	}
	if c.Status != "active" && c.Status != "inactive" {
		v.Add("status", "must be active or inactive")
	}
	return c, v.Err()
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) Create(ctx context.Context, p authz.Principal, in Input) (Customer, error) {
	if err := p.Require(authz.CustomersManage); err != nil {
		return Customer{}, err
	}
	c, err := validate(in, p.Org.BaseCurrency)
	if err != nil {
		return Customer{}, err
	}
	var out Customer
	err = tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.CreateCustomer(ctx, store.CreateCustomerParams{
			OrganizationID: p.Org.ID, Name: c.Name, ContactName: c.ContactName, Email: c.Email, Phone: c.Phone,
			AddressLine1: c.Line1, AddressLine2: c.Line2, City: c.City, Region: c.Region, PostalCode: c.PostalCode,
			Country: c.Country, Currency: c.Currency, Status: c.Status, Notes: c.Notes, Source: "internal",
		})
		if err != nil {
			return err
		}
		out = FromStore(row)
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "customer.created", EntityType: "customer", EntityID: row.ID, After: out})
	})
	return out, err
}

func (s *Service) Update(ctx context.Context, p authz.Principal, id uuid.UUID, in Input) (Customer, error) {
	if err := p.Require(authz.CustomersManage); err != nil {
		return Customer{}, err
	}
	var out Customer
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetCustomerForUpdate(ctx, id)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("customer")
		}
		if err != nil {
			return err
		}
		c, err := validate(in, cur.Currency)
		if err != nil {
			return err
		}
		if c.Currency != cur.Currency {
			used, err := q.CustomerHasInvoices(ctx, id)
			if err != nil {
				return err
			}
			if used {
				return (&apperr.Validation{}).With("currency", "cannot change once invoices have been issued")
			}
		}
		row, err := q.UpdateCustomer(ctx, store.UpdateCustomerParams{
			ID: id, Name: c.Name, ContactName: c.ContactName, Email: c.Email, Phone: c.Phone, AddressLine1: c.Line1,
			AddressLine2: c.Line2, City: c.City, Region: c.Region, PostalCode: c.PostalCode, Country: c.Country,
			Currency: c.Currency, Status: c.Status, Notes: c.Notes,
		})
		if err != nil {
			return err
		}
		out = FromStore(row)
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "customer.updated", EntityType: "customer", EntityID: id, Before: FromStore(cur), After: out})
	})
	return out, err
}

type ListResult struct {
	Data  []Customer `json:"data"`
	Total int64      `json:"total"`
}

func (s *Service) List(ctx context.Context, p authz.Principal, q string, status *string, page input.Page) (ListResult, error) {
	if err := p.Require(authz.CustomersView); err != nil {
		return ListResult{}, err
	}
	var res ListResult
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		qs := store.New(tx)
		pat := input.Pattern(q)
		rows, err := qs.ListCustomers(ctx, store.ListCustomersParams{Pattern: pat, Status: status, RowLimit: page.Limit, RowOffset: page.Offset})
		if err != nil {
			return err
		}
		res.Total, err = qs.CountCustomers(ctx, store.CountCustomersParams{Pattern: pat, Status: status})
		if err != nil {
			return err
		}
		res.Data = make([]Customer, 0, len(rows))
		for _, r := range rows {
			c := FromStore(store.Customer{
				ID: r.ID, OrganizationID: r.OrganizationID, Name: r.Name, ContactName: r.ContactName, Email: r.Email, Phone: r.Phone,
				AddressLine1: r.AddressLine1, AddressLine2: r.AddressLine2, City: r.City, Region: r.Region, PostalCode: r.PostalCode,
				Country: r.Country, Currency: r.Currency, Status: r.Status, Notes: r.Notes, Source: r.Source, ExternalID: r.ExternalID,
				CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			})
			o := money.Format(r.Outstanding, r.Currency)
			n := r.InvoiceCount
			c.Outstanding, c.InvoiceCount = &o, &n
			res.Data = append(res.Data, c)
		}
		return nil
	})
	return res, err
}

type Summary struct {
	Currency    string `json:"currency"`
	Invoiced    string `json:"invoiced"`
	Paid        string `json:"paid"`
	Outstanding string `json:"outstanding"`
	Overdue     string `json:"overdue"`
}

type InvoiceRef struct {
	ID         uuid.UUID `json:"id"`
	Number     string    `json:"number"`
	Status     string    `json:"status"`
	IsOverdue  bool      `json:"is_overdue"`
	IssueDate  string    `json:"issue_date"`
	DueDate    string    `json:"due_date"`
	Currency   string    `json:"currency"`
	Total      string    `json:"total"`
	AmountPaid string    `json:"amount_paid"`
	Balance    string    `json:"balance"`
}

type ProjectRef struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Code      *string   `json:"code"`
	Status    string    `json:"status"`
	StartDate string    `json:"start_date"`
	EndDate   *string   `json:"end_date"`
}

type Detail struct {
	Customer Customer     `json:"customer"`
	Summary  []Summary    `json:"summary"`
	Invoices []InvoiceRef `json:"invoices"`
	Projects []ProjectRef `json:"projects"`
}

func (s *Service) Get(ctx context.Context, p authz.Principal, id uuid.UUID) (Detail, error) {
	if err := p.Require(authz.CustomersView); err != nil {
		return Detail{}, err
	}
	var d Detail
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		c, err := q.GetCustomer(ctx, id)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("customer")
		}
		if err != nil {
			return err
		}
		d.Customer = FromStore(c)
		today := p.Today()
		sums, err := q.CustomerSummary(ctx, store.CustomerSummaryParams{CustomerID: id, Today: today})
		if err != nil {
			return err
		}
		d.Summary = make([]Summary, 0, len(sums))
		for _, s := range sums {
			d.Summary = append(d.Summary, Summary{Currency: s.Currency, Invoiced: money.Format(s.Invoiced, s.Currency),
				Paid: money.Format(s.Paid, s.Currency), Outstanding: money.Format(s.Outstanding, s.Currency), Overdue: money.Format(s.Overdue, s.Currency)})
		}
		d.Invoices = []InvoiceRef{}
		if p.Can(authz.InvoicesView) {
			invs, err := q.ListInvoices(ctx, store.ListInvoicesParams{Today: today, CustomerID: &id, RowLimit: 25})
			if err != nil {
				return err
			}
			for _, i := range invs {
				d.Invoices = append(d.Invoices, InvoiceRef{ID: i.ID, Number: i.Number, Status: i.Status, IsOverdue: i.IsOverdue,
					IssueDate: input.FormatDate(i.IssueDate), DueDate: input.FormatDate(i.DueDate), Currency: i.Currency,
					Total: money.Format(i.Total, i.Currency), AmountPaid: money.Format(i.AmountPaid, i.Currency),
					Balance: money.Format(i.Total.Sub(i.AmountPaid), i.Currency)})
			}
		}
		d.Projects = []ProjectRef{}
		if p.Can(authz.ProjectsView) {
			ps, err := q.ListCustomerProjects(ctx, id)
			if err != nil {
				return err
			}
			for _, pr := range ps {
				d.Projects = append(d.Projects, ProjectRef{ID: pr.ID, Name: pr.Name, Code: pr.Code, Status: pr.Status,
					StartDate: input.FormatDate(pr.StartDate), EndDate: input.FormatDatePtr(pr.EndDate)})
			}
		}
		return nil
	})
	return d, err
}

type Handler struct{ svc *Service }

func NewHandler(s *Service) *Handler { return &Handler{svc: s} }

func (h *Handler) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Patch("/{id}", h.update)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	status, err := input.QueryEnum(r, "status", "active", "inactive")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	res, err := h.svc.List(r.Context(), authz.MustPrincipal(r.Context()), r.URL.Query().Get("q"), status, input.PageFrom(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	c, err := h.svc.Create(r.Context(), authz.MustPrincipal(r.Context()), in)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"customer": c})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.URLParamUUID(r, "id")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	d, err := h.svc.Get(r.Context(), authz.MustPrincipal(r.Context()), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.URLParamUUID(r, "id")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	var in Input
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	c, err := h.svc.Update(r.Context(), authz.MustPrincipal(r.Context()), id, in)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"customer": c})
}
