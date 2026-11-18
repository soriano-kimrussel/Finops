package orgs

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"finopshq/internal/apperr"
	"finopshq/internal/audit"
	"finopshq/internal/auth"
	"finopshq/internal/authz"
	"finopshq/internal/store"
	"finopshq/internal/tenant"
)

// Provisioner seeds per-organization defaults (chart of accounts, categories, numbering)
// inside the creating transaction.
type Provisioner func(ctx context.Context, q *store.Queries, orgID uuid.UUID) error

type Service struct {
	pool        *pgxpool.Pool
	provisioner Provisioner
}

func NewService(pool *pgxpool.Pool, p Provisioner) *Service {
	return &Service{pool: pool, provisioner: p}
}

type Membership struct {
	Organization authz.Org `json:"organization"`
	Role         string    `json:"role"`
}

type CreateInput struct {
	Name         string `json:"name"`
	BaseCurrency string `json:"base_currency"`
	Timezone     string `json:"timezone"`
}

type UpdateInput struct {
	Name     *string `json:"name"`
	Timezone *string `json:"timezone"`
}

type Member struct {
	ID       uuid.UUID `json:"id"`
	UserID   uuid.UUID `json:"user_id"`
	Name     string    `json:"name"`
	Email    string    `json:"email"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

func toOrg(o store.Organization) authz.Org {
	return authz.Org{ID: o.ID, Name: o.Name, BaseCurrency: o.BaseCurrency, Timezone: o.Timezone}
}

func (s *Service) ListForUser(ctx context.Context, userID uuid.UUID) ([]Membership, error) {
	var out []Membership
	err := tenant.WithTx(ctx, s.pool, tenant.Scope{UserID: userID}, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListUserOrganizations(ctx, userID)
		if err != nil {
			return err
		}
		out = make([]Membership, 0, len(rows))
		for _, r := range rows {
			out = append(out, Membership{
				Organization: authz.Org{ID: r.ID, Name: r.Name, BaseCurrency: r.BaseCurrency, Timezone: r.Timezone},
				Role:         r.Role,
			})
		}
		return nil
	})
	return out, err
}

func validateName(v *apperr.Validation, name string) {
	if name == "" || utf8.RuneCountInString(name) > 200 {
		v.Add("name", "is required (max 200 characters)")
	}
}

func validateTimezone(ctx context.Context, tx pgx.Tx, v *apperr.Validation, tz string) error {
	if tz == "" || tz == "Local" || len(tz) > 64 {
		v.Add("timezone", "must be an IANA time zone such as Europe/London")
		return nil
	}
	if _, err := time.LoadLocation(tz); err != nil {
		v.Add("timezone", "is not a known IANA time zone")
		return nil
	}
	var ok bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_timezone_names WHERE name = $1)", tz).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		v.Add("timezone", "is not supported by the database")
	}
	return nil
}

func (s *Service) Create(ctx context.Context, user auth.User, in CreateInput) (authz.Org, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.BaseCurrency = strings.ToUpper(strings.TrimSpace(in.BaseCurrency))
	in.Timezone = strings.TrimSpace(in.Timezone)
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}

	orgID := uuid.New()
	scope := tenant.Scope{OrgID: orgID, UserID: user.ID}
	var org authz.Org
	err := tenant.WithTx(ctx, s.pool, scope, func(tx pgx.Tx) error {
		q := store.New(tx)
		var v apperr.Validation
		validateName(&v, in.Name)
		if _, err := q.GetCurrency(ctx, in.BaseCurrency); apperr.IsNoRows(err) {
			v.Add("base_currency", "is not a supported currency")
		} else if err != nil {
			return err
		}
		if err := validateTimezone(ctx, tx, &v, in.Timezone); err != nil {
			return err
		}
		if err := v.Err(); err != nil {
			return err
		}

		o, err := q.CreateOrganization(ctx, store.CreateOrganizationParams{ID: orgID, Name: in.Name, BaseCurrency: in.BaseCurrency, Timezone: in.Timezone})
		if err != nil {
			return err
		}
		m, err := q.CreateMembership(ctx, store.CreateMembershipParams{OrganizationID: orgID, UserID: user.ID, Role: string(authz.RoleOwner)})
		if err != nil {
			return err
		}
		if s.provisioner != nil {
			if err := s.provisioner(ctx, q, orgID); err != nil {
				return err
			}
		}
		org = toOrg(o)
		if err := audit.Record(ctx, q, scope, audit.Entry{Action: "organization.created", EntityType: "organization", EntityID: orgID, After: org}); err != nil {
			return err
		}
		return audit.Record(ctx, q, scope, audit.Entry{Action: "membership.added", EntityType: "membership", EntityID: m.ID,
			After: map[string]any{"user_id": user.ID, "email": user.Email, "role": m.Role}})
	})
	return org, err
}

func (s *Service) Update(ctx context.Context, p authz.Principal, in UpdateInput) (authz.Org, error) {
	if err := p.Require(authz.OrgManage); err != nil {
		return authz.Org{}, err
	}
	var org authz.Org
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetOrganization(ctx, p.Org.ID)
		if err != nil {
			return err
		}
		name, tz := cur.Name, cur.Timezone
		if in.Name != nil {
			name = strings.TrimSpace(*in.Name)
		}
		if in.Timezone != nil {
			tz = strings.TrimSpace(*in.Timezone)
		}
		var v apperr.Validation
		validateName(&v, name)
		if err := validateTimezone(ctx, tx, &v, tz); err != nil {
			return err
		}
		if err := v.Err(); err != nil {
			return err
		}
		o, err := q.UpdateOrganization(ctx, store.UpdateOrganizationParams{ID: p.Org.ID, Name: name, Timezone: tz})
		if err != nil {
			return err
		}
		org = toOrg(o)
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "organization.updated", EntityType: "organization", EntityID: o.ID,
			Before: toOrg(cur), After: org})
	})
	return org, err
}

func (s *Service) ListMembers(ctx context.Context, p authz.Principal) ([]Member, error) {
	if err := p.Require(authz.MembersView); err != nil {
		return nil, err
	}
	var out []Member
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListMembers(ctx, p.Org.ID)
		if err != nil {
			return err
		}
		out = make([]Member, 0, len(rows))
		for _, r := range rows {
			out = append(out, Member{ID: r.ID, UserID: r.UserID, Name: r.Name, Email: r.Email, Role: r.Role, JoinedAt: r.CreatedAt})
		}
		return nil
	})
	return out, err
}

// Only owners may grant the owner role or touch an owner's membership.
func checkRoleChange(p authz.Principal, current, next authz.Role) error {
	if (current == authz.RoleOwner || next == authz.RoleOwner) && p.Role != authz.RoleOwner {
		return apperr.Forbidden("only an owner can grant, change or remove the owner role")
	}
	return nil
}

func (s *Service) AddMember(ctx context.Context, p authz.Principal, email string, role authz.Role) (Member, error) {
	if err := p.Require(authz.MembersManage); err != nil {
		return Member{}, err
	}
	email = auth.NormalizeEmail(email)
	var v apperr.Validation
	if !auth.ValidEmail(email) {
		v.Add("email", "must be a valid email address")
	}
	if !role.Valid() {
		v.Add("role", "must be one of owner, admin, accountant, project_manager, viewer")
	}
	if err := v.Err(); err != nil {
		return Member{}, err
	}
	if err := checkRoleChange(p, "", role); err != nil {
		return Member{}, err
	}

	var out Member
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		u, err := q.GetUserByEmail(ctx, email)
		if apperr.IsNoRows(err) {
			return &apperr.Error{Status: http.StatusUnprocessableEntity, Code: "user_not_found",
				Message: "no account uses this email yet; ask them to sign up first", Fields: map[string]string{"email": "has no account"}}
		}
		if err != nil {
			return err
		}
		m, err := q.CreateMembership(ctx, store.CreateMembershipParams{OrganizationID: p.Org.ID, UserID: u.ID, Role: string(role)})
		if apperr.IsUniqueViolation(err, "memberships_org_user_key") {
			return apperr.Conflict("already_member", "this user is already a member of the organization")
		}
		if err != nil {
			return err
		}
		out = Member{ID: m.ID, UserID: u.ID, Name: u.Name, Email: u.Email, Role: m.Role, JoinedAt: m.CreatedAt}
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "membership.added", EntityType: "membership", EntityID: m.ID,
			After: map[string]any{"user_id": u.ID, "email": u.Email, "role": m.Role}})
	})
	return out, err
}

func (s *Service) ChangeRole(ctx context.Context, p authz.Principal, membershipID uuid.UUID, role authz.Role) (Member, error) {
	if err := p.Require(authz.MembersManage); err != nil {
		return Member{}, err
	}
	if !role.Valid() {
		return Member{}, (&apperr.Validation{}).With("role", "must be one of owner, admin, accountant, project_manager, viewer")
	}
	var out Member
	err := tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		owners, err := q.LockOwnerMemberships(ctx, p.Org.ID)
		if err != nil {
			return err
		}
		m, err := q.GetMembershipForUpdate(ctx, membershipID)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("member")
		}
		if err != nil {
			return err
		}
		if m.UserID == p.User.ID {
			return apperr.Unprocessable("cannot_change_own_role", "you cannot change your own role")
		}
		current := authz.Role(m.Role)
		if err := checkRoleChange(p, current, role); err != nil {
			return err
		}
		if current == authz.RoleOwner && role != authz.RoleOwner && len(owners) <= 1 {
			return apperr.Unprocessable("last_owner", "an organization must keep at least one owner")
		}
		updated, err := q.UpdateMembershipRole(ctx, store.UpdateMembershipRoleParams{ID: m.ID, Role: string(role)})
		if err != nil {
			return err
		}
		u, err := q.GetUserByID(ctx, m.UserID)
		if err != nil {
			return err
		}
		out = Member{ID: m.ID, UserID: u.ID, Name: u.Name, Email: u.Email, Role: updated.Role, JoinedAt: m.CreatedAt}
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "membership.role_changed", EntityType: "membership", EntityID: m.ID,
			Before: map[string]any{"role": m.Role}, After: map[string]any{"role": updated.Role}, Extra: map[string]any{"email": u.Email}})
	})
	return out, err
}

func (s *Service) RemoveMember(ctx context.Context, p authz.Principal, membershipID uuid.UUID) error {
	return tenant.WithTx(ctx, s.pool, p.Scope(), func(tx pgx.Tx) error {
		q := store.New(tx)
		owners, err := q.LockOwnerMemberships(ctx, p.Org.ID)
		if err != nil {
			return err
		}
		m, err := q.GetMembershipForUpdate(ctx, membershipID)
		if apperr.IsNoRows(err) {
			return apperr.NotFound("member")
		}
		if err != nil {
			return err
		}
		leaving := m.UserID == p.User.ID
		if !leaving {
			if err := p.Require(authz.MembersManage); err != nil {
				return err
			}
			if err := checkRoleChange(p, authz.Role(m.Role), ""); err != nil {
				return err
			}
		}
		if m.Role == string(authz.RoleOwner) && len(owners) <= 1 {
			return apperr.Unprocessable("last_owner", "an organization must keep at least one owner")
		}
		u, err := q.GetUserByID(ctx, m.UserID)
		if err != nil {
			return err
		}
		if err := q.DeleteMembership(ctx, m.ID); err != nil {
			return err
		}
		return audit.Record(ctx, q, p.Scope(), audit.Entry{Action: "membership.removed", EntityType: "membership", EntityID: m.ID,
			Before: map[string]any{"user_id": m.UserID, "email": u.Email, "role": m.Role}})
	})
}

func (s *Service) Currencies(ctx context.Context) ([]store.Currency, error) {
	return store.New(s.pool).ListCurrencies(ctx)
}
