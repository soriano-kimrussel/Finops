package seed_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"finopshq/internal/seed"
	"finopshq/internal/server"
	"finopshq/internal/testdb"
)

func TestDemoSeedProducesConsistentBooks(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	sum, err := seed.New(db.App, server.ProvisionOrganization).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Organizations != 2 || sum.Invoices < 40 || sum.Expenses < 80 || sum.Payments < 20 {
		t.Fatalf("unexpectedly small demo data set: %+v", sum)
	}

	checks := map[string]string{
		"invoice paid amount equals completed payments": `
			SELECT count(*) FROM invoices i
			WHERE i.amount_paid <> coalesce((SELECT sum(amount) FROM payments p WHERE p.invoice_id = i.id AND p.status = 'completed'), 0)`,
		"every completed payment has a ledger receipt": `
			SELECT count(*) FROM payments p
			WHERE NOT EXISTS (SELECT 1 FROM transactions t WHERE t.source_type = 'payment' AND t.source_id = p.id)`,
		"every approved expense has a ledger payment": `
			SELECT count(*) FROM expenses e WHERE e.approval_status = 'approved'
			AND NOT EXISTS (SELECT 1 FROM transactions t WHERE t.source_type = 'expense' AND t.source_id = e.id)`,
		"no unapproved expense touches the ledger": `
			SELECT count(*) FROM transactions t JOIN expenses e ON e.id = t.source_id
			WHERE t.source_type = 'expense' AND e.approval_status <> 'approved'`,
		"no payment is dated in the future": `SELECT count(*) FROM payments WHERE paid_on > current_date + 1`,
		"no payment is dated before its invoice": `
			SELECT count(*) FROM payments p JOIN invoices i ON i.id = p.invoice_id WHERE p.paid_on < i.issue_date`,
	}
	for name, sql := range checks {
		var n int
		if err := db.Owner.QueryRow(ctx, sql).Scan(&n); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if n != 0 {
			t.Errorf("%s: %d violations", name, n)
		}
	}

	var ledgerNet, expected decimal.Decimal
	if err := db.Owner.QueryRow(ctx, `SELECT coalesce(sum(CASE direction WHEN 'in' THEN amount ELSE -amount END), 0) FROM transactions`).Scan(&ledgerNet); err != nil {
		t.Fatal(err)
	}
	if err := db.Owner.QueryRow(ctx, `SELECT
		(SELECT coalesce(sum(amount),0) FROM payments WHERE status = 'completed')
		- (SELECT coalesce(sum(amount),0) FROM expenses WHERE approval_status = 'approved')`).Scan(&expected); err != nil {
		t.Fatal(err)
	}
	if !ledgerNet.Equal(expected) {
		t.Errorf("ledger net %s != payments - expenses %s", ledgerNet, expected)
	}

	if _, err := seed.New(db.App, server.ProvisionOrganization).Run(ctx); !errors.Is(err, seed.ErrAlreadySeeded) {
		t.Fatalf("second run must refuse, got %v", err)
	}
}
