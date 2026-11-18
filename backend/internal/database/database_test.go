package database_test

import (
	"context"
	"testing"

	"finopshq/internal/testdb"
)

func TestMigrationsApplyAndAppRoleIsRestricted(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	var bypass bool
	if err := db.App.QueryRow(ctx, "SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user").Scan(&bypass); err != nil {
		t.Fatal(err)
	}
	if bypass {
		t.Fatal("finops_app must not bypass row-level security")
	}

	var n int
	if err := db.App.QueryRow(ctx, "SELECT count(*) FROM currencies").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("expected seeded currencies")
	}

	if _, err := db.App.Exec(ctx, "INSERT INTO currencies (code, name, minor_units) VALUES ('XXX', 'x', 2)"); err == nil {
		t.Fatal("app role must not be able to modify reference data")
	}
}
