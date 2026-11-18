# Database schema

PostgreSQL 18. All primary keys are UUIDs (`gen_random_uuid()`). Money is `NUMERIC(19,4)` (rounded by the application to the currency's minor units), quantities are `NUMERIC(12,4)` and rates are `NUMERIC(7,4)`. Every tenant table has `organization_id`, a `UNIQUE (organization_id, id)` key for composite foreign keys, and an RLS policy. Tables carry `created_at`, plus `updated_at` maintained by a trigger where rows change.

The schema lives in `backend/db/migrations`, applied in timestamp order:

| Migration | Contents |
|---|---|
| `…203147_foundation` | extensions (`citext`, `pg_trgm`), default privileges for `finops_app`, tenant-context functions, `currencies` |
| `…205528_identity_tenancy` | `users`, `sessions`, `organizations`, `memberships`, `audit_logs` |
| `…214148_core_financials` | customers, projects, categories, budgets, invoices, items, payments, expenses, accounts, transactions |
| `…220207_integrations_sync` | `sync_connections`, `oauth_states`, `sync_jobs`, `sync_job_attempts`, `external_records`, worker and webhook functions |
| `…220732_reconciliation` | `reconciliation_runs`, `reconciliation_records` |
| `…224703` → `…233747` | the four-step `vendors` migration (see [database-migrations.md](database-migrations.md)) |

## Roles

| Role | Used by | Notes |
|---|---|---|
| `finops_owner` | migrations, backfills, test fixtures | Owns the schema; `BYPASSRLS`; `CREATEDB` for test databases |
| `finops_app` | API, worker and `seed` | DML only, no `BYPASSRLS`; `UPDATE/DELETE` revoked on immutable tables |

## Identity and tenancy

**users**: `email` (citext, unique, format check), `name`, `password_hash` (argon2id), `is_active`.

**sessions**: `user_id`, `token_hash` (SHA-256, unique), `expires_at` (check: after `created_at`), `last_seen_at`, `ip`, `user_agent`.

**organizations**: `name`, `base_currency` → `currencies`, `timezone` (validated against Go and PostgreSQL time-zone names).

**memberships**: `(organization_id, user_id)` unique, `role` ∈ owner/admin/accountant/project_manager/viewer. The service keeps at least one owner per organization, locking the owner rows before any change.

**audit_logs**: `actor_user_id` or system actor, `action` (`entity.verb` format check), `entity_type`, `entity_id`, `changes` (JSON with `before`/`after`), `request_id`, `ip`, `created_at`. Immutable: `UPDATE`, `DELETE` and `TRUNCATE` are revoked from `finops_app`, and a trigger rejects them for every role, including the owner.

**currencies**: reference data (`code`, `minor_units`), read-only for the app.

## Customers and projects

**customers**: contact and billing-address fields, `currency`, `status` (active/inactive), `source` (internal/quickbooks), `external_id`. `UNIQUE (organization_id, source, external_id)`. A trigram index on `name` serves search.

**projects**: `customer_id` (composite FK), `name`, `code` (unique per organization), `status`, `currency`, `start_date`, `end_date` (check: end ≥ start). The currency is immutable (trigger), because budgets and expenses reference it through `(organization_id, id, currency)`.

**expense_categories**: `name` unique per organization (case-insensitive), `is_archived`. New organizations get eight default categories.

**budgets**: `project_id` plus `currency` (composite FK to the project, so the currency must match), `category_id` (null = the general line), `amount > 0`, `version` for optimistic locking. `UNIQUE NULLS NOT DISTINCT (organization_id, project_id, category_id)` allows one general line per project.

## Invoicing

**invoice_number_sequences**: one row per organization (`prefix`, `next_value`). Numbering locks this row, so numbers are serialized, and a rolled-back invoice also rolls back its number. Numbers have no gaps.

**invoices**: `number` (unique per organization), `status` ∈ draft/sent/partially_paid/paid/void, `currency`, `issue_date`, `due_date`, `discount_type`/`discount_value`, `tax_rate`, the stored totals (`subtotal`, `discount_total`, `tax_total`, `total`), `amount_paid`, `version`, `source`/`external_id`. Check constraints enforce:
- `due_date >= issue_date`
- every amount is non-negative, and the discount is no greater than the subtotal
- `total = subtotal - discount_total + tax_total`
- `0 <= amount_paid <= total`
- the status agrees with the money: draft, sent and void have nothing paid; partially paid means 0 < paid < total; paid means paid = total > 0
- a void invoice has a reason and a timestamp

Two triggers guard history:
- `invoices_history_guard`: void invoices cannot change, and once money has been applied, the customer, currency, number, dates, rates and totals are frozen.
- `invoices_no_delete`: invoices are never deleted (delete is also revoked).

**invoice_items**: `position` (unique per invoice), `description`, `quantity > 0`, `unit_price >= 0`, `line_total >= 0`. A trigger rejects any change once the parent invoice has payments or is no longer draft or sent.

**payments**: `invoice_id`, `amount > 0`, `currency`, `paid_on`, `method`, `reference`, `status` (completed/reversed), `idempotency_key` (unique per organization), `request_fingerprint`, reversal fields (check: present exactly when reversed). Trigger: payments are never deleted, the amount, date, currency, invoice and key never change, and a reversed payment cannot change again.

## Expenses

**vendors**: `name`, unique per organization case-insensitively.

**expenses**: `category_id`, `project_id` (with currency, composite FK), `vendor_id` (NOT NULL, composite FK), `expense_date`, `amount > 0`, `currency`, `approval_status` (pending/approved/rejected), `submitted_by`, review fields (check: present exactly when reviewed), `version`. Trigger: a reviewed expense cannot change status; an approved expense cannot change amount, currency, date, category or project, and cannot be deleted.

## Ledger

**accounts**: a small system chart of accounts per organization (Operating Cash, Accounts Receivable, Service Revenue, Operating Expenses), `code` unique per organization.

**transactions**: one row per cash movement: `account_id`, `occurred_on`, `direction` (in/out), `amount > 0`, `currency`, `source_type` (payment/payment_reversal/expense) and `source_id`, with `UNIQUE (organization_id, source_type, source_id)`, so a source event can never post twice. Immutable (trigger, and update/delete revoked).

## Integrations

**sync_connections**: one per organization and provider. `mode` (live/simulated), `status` (connected/needs_reauth/error/disconnected), `realm_id` (unique among active connections), the AES-256-GCM encrypted `access_token_enc` and `refresh_token_enc` with their expiry times, `last_synced_at`, `last_error`. Check: a live connection that is connected has a realm and a refresh token.

**oauth_states**: SHA-256 of the state secret, the user who started the flow, `expires_at` (10 minutes), `used_at` (single use).

**sync_jobs**: `kind` (full/customers/invoices/payments), `trigger` (manual/webhook/retry), `status`, `attempts`/`max_attempts`, `run_at`, lease (`locked_by`, `locked_until`), `last_error`, `stats` (JSON), `idempotency_key` (unique per organization), `retry_of`. Partial unique index: only one active job per (connection, kind). Indexes support claiming due jobs and finding expired leases.

**sync_job_attempts**: one immutable row per attempt with outcome, error and stats.

**external_records**: QuickBooks data exactly as received (`raw` JSON) plus normalized columns used for matching (`doc_number`, dates, `amount`, `balance`, `currency`, customer and invoice references), and a `payload_hash` to detect changes. Unique per (organization, connection, entity type, external id).

## Reconciliation

**reconciliation_runs**: trigger (manual/sync), who started it, counts.

**reconciliation_records**: `match_key` (e.g. `invoice:ext:<id>`), `fingerprint` (hash of the compared values), `match_status` (matched/amount_mismatch/date_mismatch/missing_internal/missing_external), links to the external record, invoice and payment, both sides' amounts and dates, `difference`, `review_status` (open/reviewed/not_required/superseded), `decision` (accepted/ignored/follow_up/imported), `decision_note`, reviewer.
- A partial unique index allows one current (non-superseded) record per match key.
- A trigger forbids deleting records, changing a recorded decision (except revising a follow-up), and touching superseded records.
- When the compared values change, the old record is superseded and a new one opened, so the full history of a difference and every decision made about it is preserved.

## Indexes worth knowing

- `invoices (organization_id, status, due_date)`: outstanding and overdue lists, dashboard
- `invoices (organization_id, issue_date)`, `expenses (organization_id, expense_date)`, `transactions (organization_id, occurred_on DESC, created_at DESC)`: period aggregates
- `audit_logs (organization_id, created_at DESC, id DESC)`: keyset pagination of the audit log
- `sync_jobs (run_at) WHERE status IN ('queued','retrying')`: cheap job claiming
- `customers USING gin (name gin_trgm_ops)`: substring search
