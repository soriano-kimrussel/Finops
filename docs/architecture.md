# Architecture

FinOps HQ is one Go binary, one React single-page app and one PostgreSQL database. There are no microservices and no message broker: the sync queue is a PostgreSQL table. The one external dependency at runtime is QuickBooks Online, and only in live mode.

```
Browser (React SPA)
   │  JSON over HTTPS, session cookie, X-Requested-With header
   ▼
finops serve ─────────────── chi router → middleware → domain handlers → services → sqlc queries
   │  (optional) in-process sync worker                                   │
   │                                                                      ▼
finops worker ── claim_sync_job() ── QuickBooks provider (live or simulated) ── PostgreSQL 18
```

## Binary and commands

`backend/cmd/finops` is the only executable:

| Command | Purpose |
|---|---|
| `serve` | HTTP API. Starts the sync worker in-process when `WORKER_IN_PROCESS=true` (the default). |
| `worker` | Sync worker on its own, for deployments that scale it separately from the API. |
| `migrate up/down/goto/version/force` | Migrations, embedded in the binary. `down` and backwards `goto` are refused in production. |
| `seed` | Fictional demo data. Refused in production. |
| `backfill vendors` | The batched data migration from [database-migrations.md](database-migrations.md). |

`serve` and `worker` refuse to start if `DATABASE_URL` connects as a role that could bypass row-level security: a superuser, a `BYPASSRLS` role, or the owner of the tables.

## Backend layout

Code is organized by domain rather than by technical layer. Each package holds its HTTP handler, its service (business rules and transactions) and its tests:

- `auth`: argon2id passwords, sessions, login rate limiting
- `authz`: roles, the permission matrix, the organization middleware that resolves membership
- `orgs`: organizations and members
- `customers`, `projects` (also budgets and expense categories), `invoices` (also payments), `expenses`
- `money`, `budget`: pure calculation rules with unit tests
- `ledger`: posts cash movements
- `reports`: dashboard and report aggregates
- `quickbooks`: HTTP client, OAuth, webhook verification, simulated provider
- `qbsync`: connection management, job queue, worker, importer, token refresh
- `reconcile`: matching engine, runs, review decisions
- `audit`: append-only audit entries and the audit log API
- `store`: sqlc-generated queries (never edited by hand)
- `tenant`, `database`, `httpx`, `apperr`, `input`, `config`: plumbing

A request goes through:

```
Recoverer → RequestID → Logger → SecurityHeaders → CSRFGuard
  → session (auth.Middleware) → RequireUser → OrgMiddleware (membership) → handler
```

Handlers decode input and call a service with the `authz.Principal`. The service checks the permission again, validates the input, opens a tenant transaction, does its writes, records the audit entry inside the same transaction, and returns a DTO in which every amount is a decimal string.

## Tenant isolation

Isolation is layered. Any single layer failing should not expose another organization's data.

1. **Membership check.** Every organization route is under `/api/orgs/{orgID}`. `authz.OrgMiddleware` looks up the caller's membership. A non-member gets **404**, not 403, so the response doesn't even reveal that the organization exists.
2. **Permission check.** Each service method calls `principal.Require(permission)`. The frontend hides actions a role can't use, but nothing depends on that.
3. **Row-level security.** Every tenant table has `ENABLE` and `FORCE ROW LEVEL SECURITY` and a policy `organization_id = app_current_org()`. The API connects as `finops_app`, which owns nothing and has no `BYPASSRLS`. `tenant.WithTx` opens a transaction and runs `set_config('app.org_id', …, true)`. Because the setting is transaction-local, it disappears at commit or rollback and can't leak to the next request that borrows the pooled connection. A query that runs outside `WithTx` sees no tenant rows at all, so the failure mode is "empty", never "everyone's". A test checks exactly this.
4. **Composite foreign keys.** References between tenant tables are `(organization_id, id)` pairs, so a row cannot point at another tenant's row even through a bug in the application. Projects also expose `(organization_id, id, currency)`, so invoices and expenses attached to a project must use the project's currency.

Queries mostly rely on RLS for tenant filtering rather than repeating `WHERE organization_id = …` everywhere. Lookups that are not by primary key (an account by code, a payment by idempotency key, the category list) filter on `organization_id` explicitly as well.

Two narrow `SECURITY DEFINER` functions are the only way to cross tenants:
- `claim_sync_job`, for workers
- `find_quickbooks_connection`, for webhooks, which name a QuickBooks company rather than an organization

Both return just enough to set the tenant context, and all real work then happens under RLS.

Identity tables (`users`, `sessions`) are global and not tenant-scoped. Organizations and memberships have policies that let a user see the organizations they belong to.

## Authentication and sessions

- Passwords are hashed with argon2id (19 MiB, t=2, p=1). Unknown emails are checked against a dummy hash, so timing doesn't reveal which accounts exist.
- A session is a random 32-byte token in an `HttpOnly`, `SameSite=Lax` cookie (also `Secure` outside development). The database stores only its SHA-256.
- Sessions slide forward after 7 days of inactivity up to a 30-day absolute limit. `serve` prunes expired sessions hourly.
- CSRF protection: state-changing requests must carry `X-Requested-With: finops`, which a cross-site HTML form cannot set, and a foreign `Origin` is rejected. Webhooks are exempt and authenticated by HMAC signature instead.
- Login attempts are limited to 10 per 15 minutes per IP and email.

## Background jobs

Sync jobs live in `sync_jobs`:
- A worker claims a job with `UPDATE … WHERE id = (SELECT … FOR UPDATE SKIP LOCKED LIMIT 1)` and sets a lease (`locked_until`). Concurrent workers never pick the same job.
- The worker extends the lease after every page it imports. If a worker dies, its lease expires and another worker reclaims the job, incrementing `attempts`.
- Every status change (succeeded, retrying, failed) is conditional on `locked_by` still being this worker. A worker that lost its lease cannot overwrite the result of the one that took over.
- **Transient errors** (network, HTTP 5xx, throttling) reschedule the job with exponential backoff: 30 s, 60 s, 120 s, … capped at 30 minutes, with ±20% jitter, up to `max_attempts`.
- **Permanent errors** (QuickBooks validation faults, revoked authorization) fail the job immediately. A revoked refresh token also marks the connection `needs_reauth`.
- Each attempt is recorded in `sync_job_attempts`, and failed jobs can be retried manually, which creates a new job linked to the old one.
- A partial unique index allows only one queued, running or retrying job per connection and kind. Duplicate requests, double clicks and repeated webhooks get back the existing job.
- Imports are idempotent: records are upserted on `(organization, connection, entity, external id)` and rewritten only when their payload hash changes.

After a sync that touched invoices or payments, the worker runs reconciliation as the system actor.

## Frontend

- The React app talks only to `/api` (proxied by Vite in development, served by the Go binary in production when `STATIC_DIR` is set).
- TanStack Query caches server state under keys prefixed with the organization id, so cached data from one organization can never appear while viewing another.
- Forms use react-hook-form with zod for immediate feedback. The server's field errors are mapped back onto the same fields.
- The invoice editor never adds anything up itself: it asks `POST /invoices/preview` for every total.
- Amounts are formatted with `Intl.NumberFormat` from the decimal strings the API sends, so they never pass through a binary float.
- Charts convert values to numbers only to size the bars; tooltips and labels show the server's strings.

## Design decisions worth knowing

- **Overdue is derived, not stored.** It's `due_date < today` (in the organization's time zone) with money still owed. A stored status would need a scheduler to keep it true.
- **Invoices are frozen once money is applied.** Drafts and unpaid sent invoices can be edited, and every edit is audited. After a payment, the commercial terms are locked by a database trigger. To correct one, reverse the payments and void it.
- **Nothing financial is deleted.** Payments are reversed, invoices voided, approved expenses locked. Ledger rows and audit rows are immutable, enforced by triggers and revoked privileges.
- **Imports never overwrite the books.** QuickBooks invoices and payments are staged in `external_records`. Changing the books requires an explicit, audited reconciliation decision.
