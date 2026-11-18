# Database migrations

Migrations are plain SQL files in `backend/db/migrations`, embedded in the binary and applied with golang-migrate. Versions are UTC timestamps (`20261118224703_expense_vendors_expand.up.sql`). Create new ones with:

```powershell
powershell -ExecutionPolicy Bypass -File scripts\new-migration.ps1 add_something
```

The script picks a timestamp later than the newest existing migration, so the order is always right even when two people add one on the same day.

```powershell
go run ./cmd/finops migrate up          # apply everything pending
go run ./cmd/finops migrate version     # show the current version
go run ./cmd/finops migrate goto V      # move to exactly version V
go run ./cmd/finops migrate down 1      # development only; refused when APP_ENV=production
go run ./cmd/finops migrate force V     # clear a dirty flag after fixing a failed migration by hand
```

Migrations run as `finops_owner`. The application role never has DDL rights.

## Rules we follow

- **Never edit an applied migration.** Write a new one.
- **Every `up` has a `down`**, but rollback is for development. In production we roll forward.
- **Separate expand from contract.** Anything that would break code already running (dropping or renaming a column, adding NOT NULL) ships in stages, so old and new application versions can run side by side during a deploy.
- **Know the lock.** Most `ALTER TABLE` forms take an ACCESS EXCLUSIVE lock, which blocks reads and writes for as long as the statement runs. That is fine for metadata-only changes (milliseconds) and dangerous for anything that scans or rewrites a large table.
- **One `CREATE INDEX CONCURRENTLY` per file.** It can't run inside a transaction, and golang-migrate runs a multi-statement file as one implicit transaction.

## Worked example: free-text vendors → a vendors table

Expenses started with a free-text `vendor_name`. "Delta Air Lines", "delta air lines" and " Delta Air Lines " were three different vendors, and nothing could be attached to a vendor. The goal was a proper `vendors` table and a NOT NULL `expenses.vendor_id`, without downtime and without losing a single name.

### 1. Expand: `20261118224703_expense_vendors_expand`

- Create `vendors` (unique per organization on `lower(name)`), with RLS.
- `ALTER TABLE expenses ADD COLUMN vendor_id uuid`: nullable with no default, so PostgreSQL only updates the catalog. There is no table rewrite and the lock lasts milliseconds.
- Add the composite foreign key `NOT VALID`. Existing rows are not scanned; new and updated rows are checked straight away.

Code still running the previous version keeps writing `vendor_name` and ignores `vendor_id`. Nothing breaks.

### 2. Index: `20261118230142_expense_vendors_index`

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS expenses_org_vendor_idx ON expenses (organization_id, vendor_id);
```

`CONCURRENTLY` builds the index without blocking writes. It is in a file of its own because it cannot run in a transaction. If it fails partway it leaves an INVALID index: drop it and run the migration again.

### 3. Application update and backfill

The application is deployed in a version that writes **both** columns. Every new or edited expense upserts its vendor by name and sets `vendor_id`, while `vendor_name` stays populated for anything still reading it.

Existing rows are then backfilled:

```powershell
go run ./cmd/finops backfill vendors --batch 1000 --pause 50ms
```

Each batch is one short transaction:
1. Lock up to *N* expenses that still have no vendor, with `FOR UPDATE SKIP LOCKED`, so the backfill skips rows that live traffic is editing instead of waiting for them.
2. Insert any vendor names that are new for that organization (`ON CONFLICT DO NOTHING` on the case-insensitive key; names are trimmed).
3. Point the batch at its vendors.

Because every batch only touches rows `WHERE vendor_id IS NULL`, the job is **resumable**: stop it at any time and run it again. The pause between batches limits the load it puts on a busy database.

### 4. Validate

```powershell
go run ./cmd/finops backfill vendors --verify
```

This checks, across all organizations:
- the number of expenses still without a vendor
- links whose vendor name doesn't match the expense's original name (case and whitespace ignored)
- links that cross organizations
- vendors that no expense uses

The next step is deployed only when the first three are zero.

### 5. Enforce: `20261118232952_expense_vendors_enforce`

```sql
-- refuses to run while any expense still has no vendor, with a message saying what to do
ALTER TABLE expenses VALIDATE CONSTRAINT expenses_vendor_fkey;
ALTER TABLE expenses ADD CONSTRAINT expenses_vendor_id_present CHECK (vendor_id IS NOT NULL) NOT VALID;
ALTER TABLE expenses VALIDATE CONSTRAINT expenses_vendor_id_present;
ALTER TABLE expenses ALTER COLUMN vendor_id SET NOT NULL;
ALTER TABLE expenses DROP CONSTRAINT expenses_vendor_id_present;
```

- `VALIDATE CONSTRAINT` scans the table under SHARE UPDATE EXCLUSIVE, so reads and writes continue while it runs.
- A plain `SET NOT NULL` would scan the whole table under ACCESS EXCLUSIVE. Since PostgreSQL 12, a validated `CHECK (col IS NOT NULL)` lets `SET NOT NULL` skip that scan, so the exclusive lock lasts milliseconds. The temporary check is dropped afterwards.

### 6. Contract: `20261118233747_expense_vendors_contract`

Once every running instance reads vendors through `vendor_id`, which is the current code, the old column goes:

```sql
ALTER TABLE expenses DROP COLUMN vendor_name;
```

Dropping a column is metadata-only. The disk space is reclaimed gradually as rows are rewritten.

### Measured on 200,000 rows

`go run ./cmd/migrationdemo -rows 200000` replays the whole sequence on a throwaway database (`finops_migration_demo`) filled with synthetic legacy expenses across 50 organizations, with vendor names in mixed case and stray whitespace. Results on the development machine (Windows 11, PostgreSQL 18, local SSD):

| Step | Duration | Lock that blocks traffic |
|---|---|---|
| 1. expand | 28 ms | ACCESS EXCLUSIVE, metadata only |
| 2. `CREATE INDEX CONCURRENTLY` | 338 ms | none |
| 3. backfill, 100 batches of 2,000 | 18.6 s | row locks on each batch only |
| verification queries | 295 ms | none |
| 4. enforce (validate + SET NOT NULL) | 106 ms | ACCESS EXCLUSIVE only for the final `SET NOT NULL`, which needs no scan |
| 5. contract | 15 ms | ACCESS EXCLUSIVE, metadata only |

The backfill is the long step by design: it runs alongside normal traffic in short transactions. It is slower per row than a single bulk `UPDATE` because every row fires the `updated_at` and expense-guard triggers, a trade we accept for not holding locks. Your numbers will differ with hardware and table size; run the demo against a copy of production-sized data before planning a real rollout.

The same sequence is covered by an automated test (`internal/backfill/vendors_test.go`), which also checks that:
- the enforce migration refuses to run before the backfill
- a second backfill run does nothing
- case and whitespace variants of a name collapse into one vendor per organization
- rows without a vendor are rejected afterwards
- rolling back the contract migration restores every name

## Deployment sequencing

For a change that needs expand/contract, the order across releases is:

1. **Release A:** expand migration(s). Old code keeps working.
2. **Release B:** application that writes the new structure (and still the old one), and reads tolerating both.
3. **Backfill** in production, then **verify**.
4. **Release C:** enforce migration, plus application that reads only the new structure.
5. **Release D,** after every instance runs C: contract migration.

The repository holds the final state (code reading `vendor_id`, all four migrations). On a database that already holds expenses, run the steps in order:

```powershell
go run ./cmd/finops migrate goto 20261118230142   # expand + index
go run ./cmd/finops backfill vendors
go run ./cmd/finops backfill vendors --verify
go run ./cmd/finops migrate up                    # enforce + contract
```

On an empty database, such as tests or a fresh install, `migrate up` simply applies all four.

## Risks and how we handle them

| Risk | Mitigation |
|---|---|
| Long ACCESS EXCLUSIVE locks block the app | Metadata-only changes, `NOT VALID` + `VALIDATE`, `CONCURRENTLY`, the check-constraint trick for `SET NOT NULL`. Set `lock_timeout` (for example 5s) in the migration session in production, so a migration that can't get its lock fails fast instead of queueing every query behind it. |
| A migration fails halfway | Non-concurrent statements in one file run in one transaction and roll back together. golang-migrate then marks the version *dirty*: fix the cause, `migrate force <previous version>`, and re-run. |
| Data loss | Nothing is dropped until a separate, later contract migration, after verification. The contract's `down` migration can rebuild the column from `vendors`. |
| A backfill overloads the database | Small batches, `SKIP LOCKED`, a configurable pause, resumable at any point. |
| Enforcing before the data is ready | The enforce migration checks first and refuses with a clear message. |
| Old and new code running together | Expand/contract releases; the API accepts and returns `vendor_name` throughout, so clients never notice. |

## Backups and rollback

- **Before any production migration,** take a backup you have actually restored somewhere: `pg_dump -Fc` for a logical copy, or a base backup with WAL archiving for point-in-time recovery. On Windows both ship with PostgreSQL (`C:\Program Files\PostgreSQL\18\bin`).
- **Rollback limits:**
  - `down` migrations that drop columns or tables destroy data written after the `up`.
  - A dropped column comes back empty unless it can be rebuilt from elsewhere, as `vendor_name` can.
  - Rolling back application code is safe only while the schema still supports the older code, which is why the contract step comes last.
- **In production, roll forward:** fix the problem with a new migration. `migrate down` and backwards `goto` are refused when `APP_ENV=production`.

## Validation checklist for a migration

1. `go test ./...`: every test database is built from all migrations, so a broken migration fails the suite immediately.
2. Apply `up`, `down` and `up` again on a development database.
3. For data changes, run a verification query or command and record the result.
4. For large tables, time it on production-sized data (see `cmd/migrationdemo`), and check which locks each statement takes.
