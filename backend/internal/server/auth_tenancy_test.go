package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"finopshq/internal/apitest"
	"finopshq/internal/tenant"
)

func TestRegisterLoginLogout(t *testing.T) {
	env := apitest.New(t)
	c := env.Client()

	c.Must(http.StatusUnauthorized, c.Get("/api/auth/me"))
	email := c.Register("Ada Lovelace")
	me := c.Must(http.StatusOK, c.Get("/api/auth/me")).Map(t)
	if me["user"].(map[string]any)["email"] != email {
		t.Fatalf("unexpected me: %v", me)
	}

	c.Must(http.StatusNoContent, c.Post("/api/auth/logout", nil))
	c.Must(http.StatusUnauthorized, c.Get("/api/auth/me"))

	bad := c.Post("/api/auth/login", map[string]string{"email": email, "password": "wrong password here"})
	if bad.Status != http.StatusUnauthorized || bad.ErrorCode(t) != "invalid_credentials" {
		t.Fatalf("expected invalid_credentials, got %d %s", bad.Status, bad.Body)
	}
	unknown := c.Post("/api/auth/login", map[string]string{"email": "nobody@example.test", "password": "whatever whatever"})
	if unknown.ErrorCode(t) != "invalid_credentials" {
		t.Fatal("unknown email must look identical to a wrong password")
	}

	c.Must(http.StatusOK, c.Post("/api/auth/login", map[string]string{"email": "  " + email + " ", "password": "correct horse battery staple"}))
	c.Must(http.StatusOK, c.Get("/api/auth/me"))
}

func TestRegisterValidation(t *testing.T) {
	env := apitest.New(t)
	c := env.Client()
	res := c.Post("/api/auth/register", map[string]string{"name": "", "email": "nope", "password": "short"})
	if res.Status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", res.Status)
	}
	fields := res.Map(t)["error"].(map[string]any)["fields"].(map[string]any)
	for _, f := range []string{"name", "email", "password"} {
		if _, ok := fields[f]; !ok {
			t.Errorf("expected field error for %s", f)
		}
	}

	email := c.Register("Grace Hopper")
	dup := env.Client().Post("/api/auth/register", map[string]string{"name": "Other", "email": email, "password": "another long password"})
	if dup.Status != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate email, got %d", dup.Status)
	}
}

func TestSessionCookieIsHardened(t *testing.T) {
	env := apitest.New(t)
	res := env.Client().Post("/api/auth/register", map[string]string{"name": "C", "email": "cookie@example.test", "password": "correct horse battery staple"})
	cookies := (&http.Response{Header: res.Header}).Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	ck := cookies[0]
	if !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Value == "" {
		t.Fatalf("cookie not hardened: %+v", ck)
	}
	var stored int
	if err := env.DB.Owner.QueryRow(context.Background(), "SELECT count(*) FROM sessions WHERE token_hash = sha256(convert_to($1, 'UTF8'))", ck.Value).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Fatal("expected the session to be stored by token hash only")
	}
}

func TestOrganizationIsolation(t *testing.T) {
	env := apitest.New(t)
	alice, bob := env.Client(), env.Client()
	alice.Register("Alice")
	bob.Register("Bob")
	orgA := alice.CreateOrg("Acme Ltd", "USD")
	orgB := bob.CreateOrg("Bobco", "EUR")

	alice.Must(http.StatusOK, alice.Get("/api/orgs/"+orgA))
	for _, path := range []string{"/api/orgs/" + orgA, "/api/orgs/" + orgA + "/members"} {
		if res := bob.Get(path); res.Status != http.StatusNotFound {
			t.Fatalf("bob GET %s: expected 404, got %d %s", path, res.Status, res.Body)
		}
	}
	if res := bob.Patch("/api/orgs/"+orgA, map[string]string{"name": "pwned"}); res.Status != http.StatusNotFound {
		t.Fatalf("bob PATCH: expected 404, got %d", res.Status)
	}
	if res := bob.Get("/api/orgs/" + uuid.NewString()); res.Status != http.StatusNotFound {
		t.Fatal("unknown org must be 404")
	}

	var list struct {
		Data []struct {
			Organization struct{ ID string } `json:"organization"`
		} `json:"data"`
	}
	bob.Must(http.StatusOK, bob.Get("/api/orgs")).JSON(t, &list)
	if len(list.Data) != 1 || list.Data[0].Organization.ID != orgB {
		t.Fatalf("bob should only see his own org: %+v", list)
	}
}

func TestRowLevelSecurityBlocksCrossTenantReads(t *testing.T) {
	env := apitest.New(t)
	alice, bob := env.Client(), env.Client()
	alice.Register("Alice")
	bob.Register("Bob")
	orgA := uuid.MustParse(alice.CreateOrg("Acme", "USD"))
	orgB := uuid.MustParse(bob.CreateOrg("Bobco", "USD"))
	ctx := context.Background()

	count := func(s tenant.Scope, sql string, args ...any) int {
		var n int
		err := tenant.WithTx(ctx, env.DB.App, s, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, sql, args...).Scan(&n)
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	if n := count(tenant.Scope{}, "SELECT count(*) FROM memberships"); n != 0 {
		t.Fatalf("no tenant context must see 0 memberships, saw %d", n)
	}
	if n := count(tenant.Scope{}, "SELECT count(*) FROM audit_logs"); n != 0 {
		t.Fatalf("no tenant context must see 0 audit rows, saw %d", n)
	}
	if n := count(tenant.Scope{OrgID: orgB}, "SELECT count(*) FROM organizations WHERE id = $1", orgA); n != 0 {
		t.Fatal("org B context must not see org A")
	}
	if n := count(tenant.Scope{OrgID: orgB}, "SELECT count(*) FROM audit_logs WHERE organization_id = $1", orgA); n != 0 {
		t.Fatal("org B context must not see org A audit history")
	}
	if n := count(tenant.Scope{OrgID: orgA}, "SELECT count(*) FROM audit_logs"); n == 0 {
		t.Fatal("org A context should see its own audit history")
	}

	err := tenant.WithTx(ctx, env.DB.App, tenant.Scope{OrgID: orgB}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE organizations SET name = 'hijacked' WHERE id = $1", orgA)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err := env.DB.Owner.QueryRow(ctx, "SELECT name FROM organizations WHERE id = $1", orgA).Scan(&name); err != nil || name != "Acme" {
		t.Fatalf("cross-tenant update must affect no rows, name=%q err=%v", name, err)
	}

	err = tenant.WithTx(ctx, env.DB.App, tenant.Scope{OrgID: orgB}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO memberships (organization_id, user_id, role) SELECT $1, user_id, 'owner' FROM memberships LIMIT 1", orgA)
		return err
	})
	if err == nil {
		t.Fatal("inserting a membership into another tenant must violate RLS")
	}
}

func TestTenantContextDoesNotLeakAcrossPooledConnections(t *testing.T) {
	env := apitest.New(t)
	ctx := context.Background()
	orgID := uuid.New()
	err := tenant.WithTx(ctx, env.DB.App, tenant.Scope{OrgID: orgID}, func(tx pgx.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		var v *string
		if err := env.DB.App.QueryRow(ctx, "SELECT nullif(current_setting('app.org_id', true), '')").Scan(&v); err != nil {
			t.Fatal(err)
		}
		if v != nil {
			t.Fatalf("tenant setting leaked outside its transaction: %q", *v)
		}
	}
}

func TestMembershipRules(t *testing.T) {
	env := apitest.New(t)
	owner, admin, viewer, outsider := env.Client(), env.Client(), env.Client(), env.Client()
	owner.Register("Owner")
	adminEmail := admin.Register("Admin")
	viewerEmail := viewer.Register("Viewer")
	outsiderEmail := outsider.Register("Outsider")
	org := owner.CreateOrg("Rules Inc", "USD")

	adminMember := owner.AddMember(org, adminEmail, "admin")
	viewerMember := owner.AddMember(org, viewerEmail, "viewer")

	if res := viewer.Post("/api/orgs/"+org+"/members", map[string]string{"email": outsiderEmail, "role": "viewer"}); res.Status != http.StatusForbidden {
		t.Fatalf("viewer must not add members, got %d", res.Status)
	}
	if res := admin.Post("/api/orgs/"+org+"/members", map[string]string{"email": outsiderEmail, "role": "owner"}); res.Status != http.StatusForbidden {
		t.Fatalf("admin must not grant owner, got %d", res.Status)
	}
	admin.Must(http.StatusOK, admin.Patch("/api/orgs/"+org+"/members/"+viewerMember, map[string]string{"role": "accountant"}))
	if res := admin.Patch("/api/orgs/"+org+"/members/"+adminMember, map[string]string{"role": "owner"}); res.Status != http.StatusForbidden && res.Status != http.StatusUnprocessableEntity {
		t.Fatalf("admin must not self-promote, got %d", res.Status)
	}
	if res := owner.Post("/api/orgs/"+org+"/members", map[string]string{"email": adminEmail, "role": "viewer"}); res.Status != http.StatusConflict {
		t.Fatalf("duplicate membership should conflict, got %d", res.Status)
	}
	if res := owner.Post("/api/orgs/"+org+"/members", map[string]string{"email": "ghost@example.test", "role": "viewer"}); res.ErrorCode(t) != "user_not_found" {
		t.Fatalf("expected user_not_found, got %s", res.Body)
	}

	var members struct {
		Data []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"data"`
	}
	owner.Must(http.StatusOK, owner.Get("/api/orgs/"+org+"/members")).JSON(t, &members)
	var ownerMember string
	for _, m := range members.Data {
		if m.Role == "owner" {
			ownerMember = m.ID
		}
	}
	if res := admin.Delete("/api/orgs/" + org + "/members/" + ownerMember); res.Status != http.StatusForbidden {
		t.Fatalf("admin must not remove the owner, got %d", res.Status)
	}
	if res := owner.Delete("/api/orgs/" + org + "/members/" + ownerMember); res.ErrorCode(t) != "last_owner" {
		t.Fatalf("last owner must not leave, got %s", res.Body)
	}

	owner.Must(http.StatusNoContent, owner.Delete("/api/orgs/"+org+"/members/"+viewerMember))
	viewer.Must(http.StatusNotFound, viewer.Get("/api/orgs/"+org))
}

func TestAuditLogIsAppendOnly(t *testing.T) {
	env := apitest.New(t)
	c := env.Client()
	c.Register("Auditor")
	org := c.CreateOrg("Audited", "USD")
	ctx := context.Background()

	var actions []string
	rows, err := env.DB.Owner.Query(ctx, "SELECT action FROM audit_logs WHERE organization_id = $1 ORDER BY created_at", org)
	if err != nil {
		t.Fatal(err)
	}
	actions, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) < 2 || actions[0] != "organization.created" {
		t.Fatalf("unexpected audit actions %v", actions)
	}

	err = tenant.WithTx(ctx, env.DB.App, tenant.Scope{OrgID: uuid.MustParse(org)}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE audit_logs SET action = 'x.y'")
		return err
	})
	if err == nil {
		t.Fatal("app role must not update audit logs")
	}
	if _, err := env.DB.Owner.Exec(ctx, "DELETE FROM audit_logs WHERE organization_id = $1", org); err == nil {
		t.Fatal("even the owner role must not delete audit logs")
	}
}
