// Package seed creates DEMO DATA for local development: fictional organizations, people and
// transactions. Everything is created through the application services, so totals, ledger
// entries and audit history follow the same rules as real data. Never run it against production.
package seed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"finopshq/internal/apperr"
	"finopshq/internal/auth"
	"finopshq/internal/authz"
	"finopshq/internal/customers"
	"finopshq/internal/expenses"
	"finopshq/internal/invoices"
	"finopshq/internal/orgs"
	"finopshq/internal/projects"
	"finopshq/internal/store"
)

const DemoPassword = "FinOpsDemo!2026"

var ErrAlreadySeeded = errors.New("demo data already exists (owner@demo.finops.test is registered)")

type Person struct {
	Name, Email string
	Role        authz.Role
}

var northwindPeople = []Person{
	{"Morgan Reyes", "owner@demo.finops.test", authz.RoleOwner},
	{"Sam Carter", "admin@demo.finops.test", authz.RoleAdmin},
	{"Priya Shah", "accountant@demo.finops.test", authz.RoleAccountant},
	{"Daniel Okafor", "pm@demo.finops.test", authz.RoleProjectManager},
	{"Lena Novak", "viewer@demo.finops.test", authz.RoleViewer},
}

var brightlinePeople = []Person{
	{"Olivia Grant", "client@demo.finops.test", authz.RoleOwner},
}

type Seeder struct {
	pool       *pgxpool.Pool
	provision  orgs.Provisioner
	rng        *rand.Rand
	today      time.Time
	users      map[string]auth.User
	orgSvc     *orgs.Service
	custSvc    *customers.Service
	projSvc    *projects.Service
	invSvc     *invoices.Service
	expSvc     *expenses.Service
	keyCounter int
}

func New(pool *pgxpool.Pool, provision orgs.Provisioner) *Seeder {
	return &Seeder{
		pool: pool, provision: provision, rng: rand.New(rand.NewPCG(2026, 11)), users: map[string]auth.User{},
		orgSvc: orgs.NewService(pool, provision), custSvc: customers.NewService(pool), projSvc: projects.NewService(pool),
		invSvc: invoices.NewService(pool), expSvc: expenses.NewService(pool),
	}
}

type Summary struct {
	Organizations, Customers, Projects, Invoices, Payments, Expenses int
}

func (s *Seeder) Run(ctx context.Context) (Summary, error) {
	q := store.New(s.pool)
	if _, err := q.GetUserByEmail(ctx, northwindPeople[0].Email); err == nil {
		return Summary{}, ErrAlreadySeeded
	} else if !apperr.IsNoRows(err) {
		return Summary{}, err
	}
	hash, err := auth.HashPassword(DemoPassword)
	if err != nil {
		return Summary{}, err
	}
	for _, p := range append(append([]Person{}, northwindPeople...), brightlinePeople...) {
		u, err := q.CreateUser(ctx, store.CreateUserParams{Email: p.Email, Name: p.Name, PasswordHash: hash})
		if err != nil {
			return Summary{}, fmt.Errorf("create user %s: %w", p.Email, err)
		}
		s.users[p.Email] = auth.User{ID: u.ID, Email: u.Email, Name: u.Name}
	}

	var sum Summary
	if err := s.northwind(ctx, &sum); err != nil {
		return sum, fmt.Errorf("seed Northwind: %w", err)
	}
	if err := s.brightline(ctx, &sum); err != nil {
		return sum, fmt.Errorf("seed Brightline: %w", err)
	}
	return sum, nil
}

func (s *Seeder) principal(org authz.Org, p Person) authz.Principal {
	return authz.Principal{User: s.users[p.Email], Org: org, Role: p.Role}
}

func (s *Seeder) createOrg(ctx context.Context, owner Person, name, currency, tz string, members []Person) (authz.Org, error) {
	org, err := s.orgSvc.Create(ctx, s.users[owner.Email], orgs.CreateInput{Name: name, BaseCurrency: currency, Timezone: tz})
	if err != nil {
		return org, err
	}
	op := s.principal(org, owner)
	for _, m := range members {
		if _, err := s.orgSvc.AddMember(ctx, op, m.Email, m.Role); err != nil {
			return org, err
		}
	}
	s.today = op.Today()
	return org, nil
}

func (s *Seeder) date(daysAgo int) string {
	return s.today.AddDate(0, 0, -daysAgo).Format("2006-01-02")
}

func cents(c int64) string { return fmt.Sprintf("%d.%02d", c/100, c%100) }

func (s *Seeder) key() string {
	s.keyCounter++
	return fmt.Sprintf("demo-seed-%06d", s.keyCounter)
}

func ptr(s string) *string { return &s }

type customerSpec struct {
	Name, Contact, Email, Phone, City, Region, Country string
}

type catalogItem struct {
	Description string
	UnitCents   int64
	MinQty      int
	MaxQty      int
}

type projectSpec struct {
	Name, Code, Status string
	Customer           int
	StartDaysAgo       int
	EndInDays          *int
	Budget             int64
	Lines              map[string]int64
}

func days(n int) *int { return &n }

func (s *Seeder) northwind(ctx context.Context, sum *Summary) error {
	owner, admin, accountant, pm := northwindPeople[0], northwindPeople[1], northwindPeople[2], northwindPeople[3]
	org, err := s.createOrg(ctx, owner, "Northwind Advisory LLC", "USD", "America/Chicago", northwindPeople[1:])
	if err != nil {
		return err
	}
	sum.Organizations++
	op, ap, acc, pmp := s.principal(org, owner), s.principal(org, admin), s.principal(org, accountant), s.principal(org, pm)

	specs := []customerSpec{
		{"Halvorsen Freight Co.", "Erik Halvorsen", "ap@halvorsenfreight.example", "+1 312 555 0141", "Chicago", "IL", "United States"},
		{"Bluepeak Dental Group", "Dr. Aisha Rahman", "billing@bluepeakdental.example", "+1 608 555 0190", "Madison", "WI", "United States"},
		{"Cedar & Pine Hospitality", "Marco Bellini", "finance@cedarpine.example", "+1 414 555 0122", "Milwaukee", "WI", "United States"},
		{"Meridian Biotech", "Hannah Cole", "accounts.payable@meridianbio.example", "+1 317 555 0178", "Indianapolis", "IN", "United States"},
		{"Oakline Property Management", "Victor Nguyen", "invoices@oakline.example", "+1 515 555 0133", "Des Moines", "IA", "United States"},
		{"Sundial Retail Group", "Grace Kim", "ap@sundialretail.example", "+1 612 555 0164", "Minneapolis", "MN", "United States"},
		{"Kestrel Aerospace Components", "Tom Brennan", "payables@kestrelaero.example", "+1 316 555 0187", "Wichita", "KS", "United States"},
		{"Riverstone Credit Union", "Nadia Petrov", "vendor.pay@riverstonecu.example", "+1 563 555 0115", "Davenport", "IA", "United States"},
		{"Tamarack Outdoor Supply", "Lucas Ortega", "accounting@tamarack.example", "+1 218 555 0109", "Duluth", "MN", "United States"},
		{"Lumen Schools Foundation", "Rebecca Stone", "grants@lumenfoundation.example", "+1 573 555 0156", "Columbia", "MO", "United States"},
	}
	custIDs := make([]uuid.UUID, 0, len(specs))
	for i, c := range specs {
		in := customers.Input{Name: c.Name, ContactName: ptr(c.Contact), Email: ptr(c.Email), Phone: ptr(c.Phone),
			City: ptr(c.City), Region: ptr(c.Region), Country: ptr(c.Country), Currency: "USD", Status: "active"}
		cust, err := s.custSvc.Create(ctx, ap, in)
		if err != nil {
			return err
		}
		custIDs = append(custIDs, cust.ID)
		sum.Customers++
		if i == len(specs)-1 {
			in.Status = "inactive"
			if _, err := s.custSvc.Update(ctx, ap, cust.ID, in); err != nil {
				return err
			}
		}
	}

	cats, err := s.projSvc.Categories(ctx, op)
	if err != nil {
		return err
	}
	catID := map[string]uuid.UUID{}
	for _, c := range cats {
		catID[c.Name] = c.ID
	}

	projectSpecs := []projectSpec{
		{"ERP migration advisory", "HAL-ERP", "active", 0, 150, days(60), 4_200_000, map[string]int64{"Contractors": 2_400_000, "Travel": 600_000}},
		{"Practice finance clean-up", "BLU-FIN", "active", 1, 120, days(30), 1_800_000, map[string]int64{"Software & Subscriptions": 300_000}},
		{"Hotel group cost review", "CPH-COST", "active", 2, 95, days(45), 1_250_000, map[string]int64{"Travel": 450_000}},
		{"Series B financial due diligence", "MER-DD", "completed", 3, 300, days(-120), 2_600_000, nil},
		{"Fractional CFO retainer 2026", "SUN-CFO", "active", 5, 280, nil, 6_000_000, map[string]int64{"Professional Services": 1_500_000}},
		{"Grant compliance audit", "RCU-AUD", "planned", 7, 10, days(90), 900_000, nil},
		{"Inventory costing model", "TAM-INV", "on_hold", 8, 200, nil, 1_100_000, map[string]int64{"Software & Subscriptions": 200_000}},
	}
	projIDs := make([]uuid.UUID, 0, len(projectSpecs))
	for _, ps := range projectSpecs {
		var end *string
		if ps.EndInDays != nil {
			end = ptr(s.date(-*ps.EndInDays))
		}
		p, err := s.projSvc.Create(ctx, pmp, projects.Input{CustomerID: custIDs[ps.Customer].String(), Name: ps.Name, Code: ptr(ps.Code),
			Status: ps.Status, StartDate: s.date(ps.StartDaysAgo), EndDate: end,
			Description: ptr("Engagement for " + specs[ps.Customer].Name + ".")})
		if err != nil {
			return err
		}
		projIDs = append(projIDs, p.ID)
		sum.Projects++
		if _, err := s.projSvc.CreateBudget(ctx, pmp, projects.BudgetInput{ProjectID: p.ID.String(), Amount: cents(ps.Budget)}); err != nil {
			return err
		}
		for cat, amt := range ps.Lines {
			id := catID[cat].String()
			if _, err := s.projSvc.CreateBudget(ctx, pmp, projects.BudgetInput{ProjectID: p.ID.String(), CategoryID: &id, Amount: cents(amt)}); err != nil {
				return err
			}
		}
	}

	catalog := []catalogItem{
		{"Advisory hours", 18500, 6, 40},
		{"Monthly bookkeeping retainer", 240000, 1, 1},
		{"Payroll processing", 65000, 1, 2},
		{"Financial modeling", 22000, 8, 30},
		{"Quarterly tax filing", 320000, 1, 1},
		{"Fractional CFO advisory", 450000, 1, 1},
		{"Audit preparation", 19500, 10, 24},
		{"Board reporting pack", 125000, 1, 1},
	}
	projectOf := map[int]int{0: 0, 1: 1, 2: 2, 3: 3, 5: 4, 8: 6}

	// Invoices across the last twelve months. Older invoices are mostly settled; recent ones are open.
	type created struct {
		id      uuid.UUID
		total   string
		age     int
		dueDays int
	}
	var all []created
	for month := 11; month >= 0; month-- {
		n := 3 + s.rng.IntN(3)
		for k := 0; k < n; k++ {
			ci := s.rng.IntN(len(custIDs) - 1)
			age := month*30 + s.rng.IntN(28) + 1
			due := []int{14, 30, 30, 45}[s.rng.IntN(4)]
			var items []invoices.ItemInput
			for j := 0; j < 1+s.rng.IntN(3); j++ {
				it := catalog[s.rng.IntN(len(catalog))]
				qty := it.MinQty + s.rng.IntN(it.MaxQty-it.MinQty+1)
				items = append(items, invoices.ItemInput{Description: it.Description, Quantity: fmt.Sprint(qty), UnitPrice: cents(it.UnitCents)})
			}
			in := invoices.Input{CustomerID: custIDs[ci].String(), IssueDate: s.date(age), DueDate: s.date(age - due), Items: items, TaxRate: "0"}
			if pi, ok := projectOf[ci]; ok && s.rng.IntN(3) > 0 {
				id := projIDs[pi].String()
				in.ProjectID = &id
			}
			if s.rng.IntN(5) == 0 {
				in.DiscountType, in.DiscountValue = "percent", "5"
			}
			if ci == 2 || ci == 5 {
				in.TaxRate = "6.25"
			}
			inv, err := s.invSvc.Create(ctx, acc, in)
			if err != nil {
				return err
			}
			sum.Invoices++
			all = append(all, created{id: inv.ID, total: inv.Total, age: age, dueDays: due})
		}
	}

	for i, c := range all {
		switch {
		case c.age < 4 && i%2 == 0:
			continue // recent drafts
		case i == 7:
			if _, err := s.invSvc.Void(ctx, acc, c.id, "Issued to the wrong billing entity; replaced by a corrected invoice.", nil); err != nil {
				return err
			}
			continue
		}
		if _, err := s.invSvc.Send(ctx, acc, c.id, nil); err != nil {
			return err
		}
		overdueAge := c.age - c.dueDays
		roll := s.rng.IntN(100)
		payDay := c.age - s.rng.IntN(max(1, min(c.dueDays+10, c.age)))
		if payDay < 0 {
			payDay = 0
		}
		switch {
		case c.age > 75 && roll < 88, c.age > 30 && roll < 60, roll < 15:
			if err := s.pay(ctx, acc, c.id, c.total, payDay, sum); err != nil {
				return err
			}
		case overdueAge > 0 && roll < 80:
			// Partially paid, balance still open.
			if err := s.payPart(ctx, acc, c.id, c.total, payDay, sum); err != nil {
				return err
			}
		}
	}

	// One payment recorded by mistake and reversed, to show the audit trail.
	if len(all) > 3 {
		inv, err := s.invSvc.Get(ctx, acc, all[len(all)-3].id)
		if err != nil {
			return err
		}
		if inv.Status == "sent" {
			res, err := s.invSvc.RecordPayment(ctx, acc, inv.ID, s.key(), invoices.PaymentInput{Amount: inv.Balance, PaidOn: s.date(1), Method: "check", Reference: ptr("CHK 4471")})
			if err != nil {
				return err
			}
			sum.Payments++
			if _, err := s.invSvc.ReversePayment(ctx, acc, res.Payment.ID, "Check returned unpaid by the bank."); err != nil {
				return err
			}
		}
	}

	type vendor struct {
		Name, Category string
		MinCents       int64
		MaxCents       int64
	}
	vendors := []vendor{
		{"Delta Air Lines", "Travel", 28000, 96000},
		{"Marriott Hotels", "Travel", 18000, 74000},
		{"Enterprise Rent-A-Car", "Travel", 9000, 32000},
		{"Amazon Web Services", "Software & Subscriptions", 12000, 46000},
		{"Microsoft 365", "Software & Subscriptions", 8800, 26400},
		{"Adobe Creative Cloud", "Software & Subscriptions", 5999, 8999},
		{"J. Alvarez Consulting", "Contractors", 180000, 640000},
		{"Brightwork Data Services", "Contractors", 95000, 310000},
		{"Staples Business", "Office & Facilities", 4500, 38000},
		{"Regus Coworking", "Office & Facilities", 65000, 120000},
		{"LinkedIn Ads", "Marketing", 25000, 150000},
		{"Hartley & Moss CPAs", "Professional Services", 120000, 450000},
		{"Dell Technologies", "Hardware", 89000, 260000},
	}
	submitters := []authz.Principal{pmp, op, ap}
	for month := 11; month >= 0; month-- {
		n := 7 + s.rng.IntN(5)
		for k := 0; k < n; k++ {
			v := vendors[s.rng.IntN(len(vendors))]
			age := month*30 + s.rng.IntN(28)
			amount := v.MinCents + s.rng.Int64N(v.MaxCents-v.MinCents+1)
			var proj *string
			if s.rng.IntN(10) < 6 {
				pi := s.rng.IntN(len(projIDs))
				if projectSpecs[pi].Status != "planned" {
					id := projIDs[pi].String()
					proj = &id
				}
			}
			sub := submitters[s.rng.IntN(len(submitters))]
			e, err := s.expSvc.Create(ctx, sub, expenses.Input{CategoryID: catID[v.Category].String(), ProjectID: proj, VendorName: v.Name,
				ExpenseDate: s.date(age), Amount: cents(amount), Description: ptr(expenseNote(v.Category))})
			if err != nil {
				return err
			}
			sum.Expenses++
			switch {
			case age < 10 && s.rng.IntN(2) == 0:
				// left pending for review
			case s.rng.IntN(25) == 0:
				if _, err := s.expSvc.Review(ctx, acc, e.ID, false, ptr("Personal expense; not reimbursable under the travel policy.")); err != nil {
					return err
				}
			default:
				if _, err := s.expSvc.Review(ctx, acc, e.ID, true, nil); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func expenseNote(category string) string {
	switch category {
	case "Travel":
		return "Client site visit"
	case "Software & Subscriptions":
		return "Monthly subscription"
	case "Contractors":
		return "Subcontracted analysis work"
	case "Marketing":
		return "Campaign spend"
	case "Professional Services":
		return "External specialist review"
	case "Hardware":
		return "Laptop for new analyst"
	default:
		return "Office supplies"
	}
}

func (s *Seeder) pay(ctx context.Context, p authz.Principal, id uuid.UUID, amount string, daysAgo int, sum *Summary) error {
	methods := []string{"bank_transfer", "bank_transfer", "card", "check"}
	_, err := s.invSvc.RecordPayment(ctx, p, id, s.key(), invoices.PaymentInput{Amount: amount, PaidOn: s.date(daysAgo),
		Method: methods[s.rng.IntN(len(methods))], Reference: ptr(fmt.Sprintf("REF-%05d", s.rng.IntN(99999)))})
	if err == nil {
		sum.Payments++
	}
	return err
}

// payPart pays roughly 40% of the invoice using integer cents, so no rounding is involved.
func (s *Seeder) payPart(ctx context.Context, p authz.Principal, id uuid.UUID, total string, daysAgo int, sum *Summary) error {
	var whole, frac int64
	if _, err := fmt.Sscanf(total, "%d.%d", &whole, &frac); err != nil {
		return err
	}
	part := (whole*100 + frac) * 4 / 10
	if part <= 0 {
		return nil
	}
	return s.pay(ctx, p, id, cents(part), daysAgo, sum)
}

func (s *Seeder) brightline(ctx context.Context, sum *Summary) error {
	owner := brightlinePeople[0]
	org, err := s.createOrg(ctx, owner, "Brightline Studio Ltd", "GBP", "Europe/London", nil)
	if err != nil {
		return err
	}
	sum.Organizations++
	p := s.principal(org, owner)
	names := []string{"Fernwood Architects", "Calder Street Bakery", "Ashby Rowing Club"}
	var ids []uuid.UUID
	for _, n := range names {
		c, err := s.custSvc.Create(ctx, p, customers.Input{Name: n, Currency: "GBP", Country: ptr("United Kingdom")})
		if err != nil {
			return err
		}
		ids = append(ids, c.ID)
		sum.Customers++
	}
	for i := 0; i < 6; i++ {
		age := 20 + i*25
		inv, err := s.invSvc.Create(ctx, p, invoices.Input{CustomerID: ids[i%3].String(), IssueDate: s.date(age), DueDate: s.date(age - 30), TaxRate: "20",
			Items: []invoices.ItemInput{{Description: "Brand identity design", Quantity: fmt.Sprint(2 + i), UnitPrice: "450.00"}}})
		if err != nil {
			return err
		}
		sum.Invoices++
		if _, err := s.invSvc.Send(ctx, p, inv.ID, nil); err != nil {
			return err
		}
		if i >= 2 {
			if err := s.pay(ctx, p, inv.ID, inv.Total, age-20, sum); err != nil {
				return err
			}
		}
	}
	slog.Info("seeded demo organization", "name", org.Name)
	return nil
}
