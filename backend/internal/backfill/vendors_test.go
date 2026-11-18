package backfill_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"

	"finopshq/internal/backfill"
	"finopshq/internal/database"
	"finopshq/internal/testdb"
)

const (
	beforeVendors = 20261118220732 // last migration before the vendor change
	expanded      = 20261118230142 // vendors table, nullable vendor_id, concurrent index
	enforced      = 20261118232952 // vendor_id NOT NULL, FK validated
	contracted    = 20261118233747 // vendor_name dropped
)

// legacyData creates two organizations whose expenses only have free-text vendor names,
// including case and whitespace variants of the same vendor.
const legacyData = `
INSERT INTO organizations (id, name, base_currency) VALUES
  ('11111111-1111-1111-1111-111111111111', 'Org A', 'USD'),
  ('22222222-2222-2222-2222-222222222222', 'Org B', 'USD');
INSERT INTO expense_categories (id, organization_id, name) VALUES
  ('aaaaaaaa-0000-0000-0000-000000000001', '11111111-1111-1111-1111-111111111111', 'Travel'),
  ('bbbbbbbb-0000-0000-0000-000000000001', '22222222-2222-2222-2222-222222222222', 'Travel');
INSERT INTO expenses (organization_id, category_id, vendor_name, expense_date, amount, currency)
SELECT CASE WHEN g % 2 = 0 THEN '11111111-1111-1111-1111-111111111111'::uuid ELSE '22222222-2222-2222-2222-222222222222'::uuid END,
       CASE WHEN g % 2 = 0 THEN 'aaaaaaaa-0000-0000-0000-000000000001'::uuid ELSE 'bbbbbbbb-0000-0000-0000-000000000001'::uuid END,
       (ARRAY['Delta Air Lines', 'delta air lines', '  Delta Air Lines ', 'Staples', 'Marriott Hotels'])[1 + g % 5],
       DATE '2026-01-01' + (g % 300),
       (10 + g % 990)::numeric + 0.25,
       'USD'
FROM generate_series(1, 5000) g;`

func TestExpandBackfillEnforceContract(t *testing.T) {
	url := testdb.NewEmpty(t)
	m, err := database.NewMigrator(url)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx := context.Background()

	if err := m.Migrate(beforeVendors); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, legacyData); err != nil {
		t.Fatal(err)
	}

	// Expand: old rows are untouched and new code paths can start writing vendor_id.
	if err := m.Migrate(expanded); err != nil {
		t.Fatal(err)
	}
	var nulls int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM expenses WHERE vendor_id IS NULL").Scan(&nulls)
	if nulls != 5000 {
		t.Fatalf("expand must not touch existing rows, %d nulls", nulls)
	}

	// Enforcing before the backfill is refused with a clear message.
	err = m.Migrate(enforced)
	if err == nil || !strings.Contains(err.Error(), "run \"finops backfill vendors\"") {
		t.Fatalf("enforce must refuse unbackfilled data, got %v", err)
	}
	if err := m.Force(expanded); err != nil {
		t.Fatal(err)
	}

	st, err := backfill.Vendors(ctx, pool, 700, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Rows != 5000 || st.Batches != 8 || st.VendorsCreated != 6 {
		t.Fatalf("backfill stats %+v", st)
	}
	again, err := backfill.Vendors(ctx, pool, 700, 0, nil)
	if err != nil || again.Rows != 0 {
		t.Fatalf("a second run must be a no-op: %+v %v", again, err)
	}
	check, err := backfill.VerifyVendors(ctx, pool)
	if err != nil || !check.OK() || check.Expenses != 5000 {
		t.Fatalf("verification %+v %v", check, err)
	}
	var vendorsPerOrg int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM vendors WHERE organization_id = '11111111-1111-1111-1111-111111111111'").Scan(&vendorsPerOrg)
	if vendorsPerOrg != 3 {
		t.Fatalf("case and whitespace variants must collapse into one vendor: %d", vendorsPerOrg)
	}

	if err := m.Migrate(enforced); err != nil {
		t.Fatal(err)
	}
	var notNull bool
	_ = pool.QueryRow(ctx, "SELECT attnotnull FROM pg_attribute WHERE attrelid = 'expenses'::regclass AND attname = 'vendor_id'").Scan(&notNull)
	if !notNull {
		t.Fatal("vendor_id must be NOT NULL after enforce")
	}
	if _, err := pool.Exec(ctx, "INSERT INTO expenses (organization_id, category_id, vendor_name, expense_date, amount, currency) VALUES ('11111111-1111-1111-1111-111111111111', 'aaaaaaaa-0000-0000-0000-000000000001', 'x', '2026-01-01', 1, 'USD')"); err == nil {
		t.Fatal("rows without vendor_id must be rejected after enforce")
	}

	if err := m.Migrate(contracted); err != nil {
		t.Fatal(err)
	}
	var cols int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM information_schema.columns WHERE table_name = 'expenses' AND column_name = 'vendor_name'").Scan(&cols)
	if cols != 0 {
		t.Fatal("contract must drop vendor_name")
	}

	// Rolling the contract back restores the names from the vendors table: nothing was lost.
	if err := m.Steps(-1); err != nil {
		t.Fatal(err)
	}
	var restored int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM expenses e JOIN vendors v ON v.id = e.vendor_id WHERE e.vendor_name = v.name").Scan(&restored)
	if restored != 5000 {
		t.Fatalf("down migration should restore every name, got %d", restored)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatal(err)
	}
}
