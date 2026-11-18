// Command migrationdemo replays the vendor schema evolution on a throwaway database
// (finops_migration_demo) filled with synthetic legacy expenses, and times every step.
//
//	go run ./cmd/migrationdemo -rows 200000
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"finopshq/internal/backfill"
	"finopshq/internal/config"
	"finopshq/internal/database"
)

const (
	dbName        = "finops_migration_demo"
	beforeVendors = 20261118220732
	expandVersion = 20261118224703
	indexVersion  = 20261118230142
	enforce       = 20261118232952
	contract      = 20261118233747
)

func main() {
	rows := flag.Int("rows", 200000, "legacy expenses to generate")
	orgs := flag.Int("orgs", 50, "organizations to spread them across")
	batch := flag.Int("batch", 2000, "backfill batch size")
	flag.Parse()
	if err := run(*rows, *orgs, *batch); err != nil {
		fmt.Fprintln(os.Stderr, "migrationdemo:", err)
		os.Exit(1)
	}
}

func step(name string, fn func() error) error {
	start := time.Now()
	if err := fn(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	fmt.Printf("| %-48s | %10s |\n", name, time.Since(start).Round(time.Millisecond))
	return nil
}

func run(rows, orgs, batch int) error {
	config.LoadDotenv()
	admin := os.Getenv("TEST_DATABASE_URL")
	if admin == "" {
		return fmt.Errorf("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return err
	}
	_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		return err
	}
	conn.Close(ctx)
	u, _ := url.Parse(admin)
	u.Path = "/" + dbName
	target := u.String()

	m, err := database.NewMigrator(target)
	if err != nil {
		return err
	}
	defer m.Close()
	pool, err := database.Open(ctx, target)
	if err != nil {
		return err
	}
	defer pool.Close()

	fmt.Printf("Vendor migration demo: %d legacy expenses across %d organizations\n\n", rows, orgs)
	fmt.Printf("| %-48s | %10s |\n|%s|%s|\n", "Step", "Duration", "--------------------------------------------------", "------------")
	steps := []struct {
		name string
		fn   func() error
	}{
		{"schema before the change", func() error { return m.Migrate(beforeVendors) }},
		{fmt.Sprintf("generate %d legacy expenses", rows), func() error {
			_, err := pool.Exec(ctx, `
				INSERT INTO organizations (name, base_currency) SELECT 'Demo org ' || g, 'USD' FROM generate_series(1, $1) g;
			`, orgs)
			if err != nil {
				return err
			}
			if _, err := pool.Exec(ctx, `INSERT INTO expense_categories (organization_id, name) SELECT id, 'General' FROM organizations`); err != nil {
				return err
			}
			_, err = pool.Exec(ctx, `
				WITH o AS (SELECT o.id, c.id AS category_id, row_number() OVER (ORDER BY o.id) - 1 AS n
				           FROM organizations o JOIN expense_categories c ON c.organization_id = o.id)
				INSERT INTO expenses (organization_id, category_id, vendor_name, expense_date, amount, currency, approval_status, reviewed_at, reviewed_by)
				SELECT o.id, o.category_id,
				       (ARRAY['Delta Air Lines','Marriott Hotels','Amazon Web Services','Staples','Dell Technologies','  delta air lines','LinkedIn Ads','Regus Coworking'])[1 + (g % 8)] || CASE WHEN g % 97 = 0 THEN ' #' || (g % 40) ELSE '' END,
				       DATE '2025-01-01' + (g % 680), (5 + g % 2000)::numeric + 0.99, 'USD', 'pending', NULL, NULL
				FROM generate_series(1, $1) g JOIN o ON o.n = g % $2`, rows, orgs)
			if err == nil {
				_, err = pool.Exec(ctx, "ANALYZE expenses")
			}
			return err
		}},
		{"1. expand: vendors table + nullable vendor_id", func() error { return m.Migrate(expandVersion) }},
		{"2. CREATE INDEX CONCURRENTLY", func() error { return m.Migrate(indexVersion) }},
		{fmt.Sprintf("3. backfill in batches of %d", batch), func() error {
			st, err := backfill.Vendors(ctx, pool, batch, 0, nil)
			if err == nil {
				fmt.Printf("|   %-46s | %10s |\n", fmt.Sprintf("%d rows, %d batches, %d vendors", st.Rows, st.Batches, st.VendorsCreated), "")
			}
			return err
		}},
		{"   verification queries", func() error {
			c, err := backfill.VerifyVendors(ctx, pool)
			if err == nil && !c.OK() {
				err = fmt.Errorf("verification failed: %+v", c)
			}
			return err
		}},
		{"4. enforce: validate FK, SET NOT NULL via CHECK", func() error { return m.Migrate(enforce) }},
		{"5. contract: drop vendor_name", func() error { return m.Migrate(contract) }},
	}
	for _, s := range steps {
		if err := step(s.name, s.fn); err != nil {
			return err
		}
	}
	fmt.Println("\nDone. The demo database is left in place for inspection:", dbName)
	return nil
}
