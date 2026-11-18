// Package backfill holds one-off data migrations that run between schema migrations.
package backfill

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type VendorStats struct {
	Batches        int
	Rows           int64
	VendorsCreated int64
	Elapsed        time.Duration
}

// Vendors links every expense to a vendors row, creating vendors from the distinct names.
// It is meant for the window between the expand and enforce migrations (while expenses still
// has vendor_name). Each batch is one short transaction; FOR UPDATE SKIP LOCKED keeps it from
// waiting on rows that live traffic is editing, and the WHERE vendor_id IS NULL filter makes the
// job resumable: stopping and restarting simply continues where it left off.
//
// It runs with the schema owner's connection because it works across all organizations.
func Vendors(ctx context.Context, pool *pgxpool.Pool, batchSize int, pause time.Duration, progress func(VendorStats)) (VendorStats, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	start := time.Now()
	var st VendorStats
	for {
		var rows, created int64
		err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			idRows, err := tx.Query(ctx, `
				SELECT id FROM expenses
				WHERE vendor_id IS NULL
				ORDER BY id
				LIMIT $1
				FOR UPDATE SKIP LOCKED`, batchSize)
			if err != nil {
				return fmt.Errorf("select batch: %w", err)
			}
			ids, err := pgx.CollectRows(idRows, pgx.RowTo[[16]byte])
			if err != nil || len(ids) == 0 {
				return err
			}
			tag, err := tx.Exec(ctx, `
				INSERT INTO vendors (organization_id, name)
				SELECT DISTINCT ON (organization_id, lower(btrim(vendor_name))) organization_id, btrim(vendor_name)
				FROM expenses WHERE id = ANY($1)
				ORDER BY organization_id, lower(btrim(vendor_name)), btrim(vendor_name)
				ON CONFLICT (organization_id, lower(name)) DO NOTHING`, ids)
			if err != nil {
				return fmt.Errorf("insert vendors: %w", err)
			}
			created = tag.RowsAffected()
			tag, err = tx.Exec(ctx, `
				UPDATE expenses e SET vendor_id = v.id
				FROM vendors v
				WHERE e.id = ANY($1) AND e.vendor_id IS NULL
				  AND v.organization_id = e.organization_id AND lower(v.name) = lower(btrim(e.vendor_name))`, ids)
			if err != nil {
				return fmt.Errorf("link expenses: %w", err)
			}
			rows = tag.RowsAffected()
			return nil
		})
		if err != nil {
			return st, err
		}
		if rows == 0 {
			st.Elapsed = time.Since(start)
			return st, nil
		}
		st.Batches++
		st.Rows += rows
		st.VendorsCreated += created
		st.Elapsed = time.Since(start)
		if progress != nil {
			progress(st)
		}
		if pause > 0 {
			select {
			case <-ctx.Done():
				return st, ctx.Err()
			case <-time.After(pause):
			}
		}
	}
}

type VendorCheck struct {
	Expenses        int64
	MissingVendorID int64
	NameMismatches  int64
	OrphanVendors   int64
	CrossTenant     int64
}

func (c VendorCheck) OK() bool {
	return c.MissingVendorID == 0 && c.NameMismatches == 0 && c.CrossTenant == 0
}

// VerifyVendors checks the backfill before the enforce migration: every expense is linked, the
// linked vendor carries the same name, and no link crosses organizations.
func VerifyVendors(ctx context.Context, pool *pgxpool.Pool) (VendorCheck, error) {
	var c VendorCheck
	err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM expenses),
			(SELECT count(*) FROM expenses WHERE vendor_id IS NULL),
			(SELECT count(*) FROM expenses e JOIN vendors v ON v.id = e.vendor_id WHERE lower(v.name) <> lower(btrim(e.vendor_name))),
			(SELECT count(*) FROM vendors v WHERE NOT EXISTS (SELECT 1 FROM expenses e WHERE e.vendor_id = v.id)),
			(SELECT count(*) FROM expenses e JOIN vendors v ON v.id = e.vendor_id WHERE v.organization_id <> e.organization_id)`).
		Scan(&c.Expenses, &c.MissingVendorID, &c.NameMismatches, &c.OrphanVendors, &c.CrossTenant)
	return c, err
}
