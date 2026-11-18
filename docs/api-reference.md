# API reference

All endpoints are under `/api` and exchange JSON.

## Conventions

- **Authentication.** A session cookie (`finops_session`) set by `/auth/register` or `/auth/login`.
- **CSRF.** Every non-GET request must send `X-Requested-With: finops`. A cross-site `Origin` is rejected. Webhooks are the exception.
- **Tenancy.** Organization data lives under `/api/orgs/{orgId}/…`. If you are not a member, every such URL returns **404**.
- **Money.** Amounts are strings with the currency's number of decimals (`"1250.00"`, `"980"` for JPY). Send amounts as strings too. Exponents, thousands separators and symbols are rejected.
- **Dates** are `YYYY-MM-DD`. Timestamps are RFC 3339.
- **Lists** take `limit` (default 50, max 200) and `offset`, and return `{ "data": [...], "total": n }`. The audit log uses a cursor instead.
- **Errors** look like this:

  ```json
  { "error": { "code": "validation_failed", "message": "some fields are invalid", "fields": { "items.0.quantity": "must be greater than zero" } } }
  ```

| Status | Meaning |
|---|---|
| 400 | malformed request or filter |
| 401 | not signed in |
| 403 | signed in, but your role lacks the permission |
| 404 | not found or not yours |
| 409 | conflict: duplicate, stale `version`, idempotency key reused for different content |
| 422 | valid JSON but a business rule says no (`overpayment`, `invoice_locked`, `invalid_decision`, …) |
| 429 | too many login attempts |

The permission each endpoint needs is shown in brackets. Roles map to permissions as follows:

| Permission | Owner | Admin | Accountant | Project manager | Viewer |
|---|:-:|:-:|:-:|:-:|:-:|
| view customers, projects, budgets, invoices, expenses, reports, members | ✓ | ✓ | ✓ | ✓ | ✓ |
| manage customers, invoices; record payments; approve expenses | ✓ | ✓ | ✓ | | |
| manage projects and budgets | ✓ | ✓ | | ✓ | |
| submit expenses | ✓ | ✓ | ✓ | ✓ | |
| run sync, view integrations, review reconciliation, view audit log | ✓ | ✓ | ✓ | | |
| manage integrations, members, organization | ✓ | ✓ | | | |

Only owners can grant, change or remove the owner role, and an organization always keeps at least one owner.

## Health

| | |
|---|---|
| `GET /healthz` | process is up |
| `GET /readyz` | database reachable (503 otherwise) |

## Authentication

| | |
|---|---|
| `POST /auth/register` `{name, email, password}` | creates the account and signs in. The password needs at least 12 characters. 409 `email_taken`. |
| `POST /auth/login` `{email, password}` | 401 `invalid_credentials`; 429 after 10 attempts in 15 minutes |
| `POST /auth/logout` | 204 |
| `GET /auth/me` | `{user, memberships: [{organization, role}]}` |
| `GET /currencies` | supported currencies with minor units |

## Organizations and members

| | |
|---|---|
| `GET /orgs` | organizations you belong to |
| `POST /orgs` `{name, base_currency, timezone}` | you become the owner. Default accounts, categories and invoice numbering are created. |
| `GET /orgs/{org}` | `{organization, role, permissions}` |
| `PATCH /orgs/{org}` `{name?, timezone?}` | [org.manage]. The base currency is fixed. |
| `GET /orgs/{org}/members` | [members.view] |
| `POST /orgs/{org}/members` `{email, role}` | [members.manage]. The user must already have an account (422 `user_not_found`). |
| `PATCH /orgs/{org}/members/{id}` `{role}` | [members.manage]. 422 `last_owner`, `cannot_change_own_role`. |
| `DELETE /orgs/{org}/members/{id}` | [members.manage], or yourself to leave |

## Customers

| | |
|---|---|
| `GET /customers?q&status` | [customers.view]. Includes `outstanding` and `invoice_count`. |
| `POST /customers` | [customers.manage] `{name, contact_name?, email?, phone?, address_line1?, address_line2?, city?, region?, postal_code?, country?, currency?, status?, notes?}` |
| `GET /customers/{id}` | `{customer, summary[per currency], invoices, projects}` |
| `PATCH /customers/{id}` | same body as create. The currency can't change once invoices are issued. |

## Projects, budgets and categories

| | |
|---|---|
| `GET /projects?q&status&customer_id` | [projects.view]. Each project includes `budget {total, actual, pending, remaining, percent_used, level}`. |
| `POST /projects` | [projects.manage] `{customer_id, name, code?, description?, status?, currency?, start_date, end_date?}` |
| `GET /projects/{id}` | `{project, budget_lines[], unbudgeted, billing}` |
| `PATCH /projects/{id}` | the currency can't change |
| `GET /budgets?project_id` | [budgets.view]. Budgeted projects with lines and consumption. |
| `POST /budgets` `{project_id, category_id?, amount, notes?}` | [budgets.manage]. No `category_id` means the general line. 409 `budget_exists`. |
| `PATCH /budgets/{id}` `{amount, notes?, version}` | 409 `stale_version` |
| `DELETE /budgets/{id}` | |
| `GET /expense-categories`, `POST /expense-categories {name}` | |

`level` is `none`, `ok`, `warning` (80% to 100% used) or `over` (above 100%).

## Invoices and payments

| | |
|---|---|
| `GET /invoices?status&customer_id&project_id&q&from&to` | [invoices.view]. `status` also accepts `overdue` and `outstanding`. |
| `POST /invoices/preview` | totals for an unsaved invoice; nothing is stored |
| `POST /invoices` | [invoices.manage]. Creates a **draft** with the next number. |
| `GET /invoices/{id}` | includes `items`, `payments`, `balance`, `display_status`, `is_overdue` |
| `PATCH /invoices/{id}` | draft, or sent with nothing paid; send `version`. 422 `invoice_locked`, 409 `stale_version`. |
| `POST /invoices/{id}/send` `{version?}` | draft → sent; the total must be above zero |
| `POST /invoices/{id}/void` `{reason, version?}` | 422 `has_payments` |
| `POST /invoices/{id}/payments` | [payments.record]. Needs the `Idempotency-Key` header. |
| `GET /payments?from&to&method&status&q` | [invoices.view] |
| `POST /payments/{id}/reverse` `{reason}` | [payments.record] |

Invoice body:

```json
{
  "customer_id": "…", "project_id": null, "currency": "USD",
  "issue_date": "2026-11-01", "due_date": "2026-12-01",
  "discount_type": "percent", "discount_value": "10", "tax_rate": "8.25",
  "notes": "Thank you", "version": 3,
  "items": [{ "description": "Advisory", "quantity": "12", "unit_price": "185.00" }]
}
```

Payment:

```http
POST /api/orgs/{org}/invoices/{id}/payments
Idempotency-Key: 6f1c0f7e-…
X-Requested-With: finops

{ "amount": "650.00", "paid_on": "2026-11-18", "method": "bank_transfer", "reference": "ACH 88213" }
```

- **201:** a new payment, returned as `{payment, invoice, replayed: false}`.
- **200:** the same key with the same content. The response carries `Idempotent-Replayed: true` and the original payment.
- **409** `idempotency_conflict`: the same key with different content.
- **422:** `overpayment`, `invoice_not_sent`, `invoice_paid`, `invoice_void`.

## Expenses and vendors

| | |
|---|---|
| `GET /expenses?q&status&category_id&project_id&from&to` | [expenses.view]. Includes `total_amount` (base currency). |
| `POST /expenses` | [expenses.submit] `{category_id, project_id?, vendor_name, expense_date, amount, currency?, description?}`. The currency defaults to the project's, then the organization's. |
| `PATCH /expenses/{id}`, `DELETE /expenses/{id}` | pending only; the submitter or an approver |
| `POST /expenses/{id}/approve` `{note?}` | [expenses.approve]. You can't approve your own expense unless you're an owner. |
| `POST /expenses/{id}/reject` `{note}` | the note is required |
| `GET /vendors` | vendor names with usage counts |

## Dashboard and reports

| | |
|---|---|
| `GET /dashboard?from&to` | [reports.view]. Totals, monthly series, budget health, recent ledger transactions, recent sync jobs. The default period is the last 12 months; the maximum is 3 years. |
| `GET /reports` | the list of reports |
| `GET /reports/{key}?from&to[&format=csv]` | `key`: `profit-and-loss`, `revenue`, `expenses`, `cash-flow`, `accounts-receivable`, `outstanding-invoices`, `budget-vs-actual`, `monthly-summary` |

A report is returned as:

```json
{ "key": "expenses", "title": "...", "currency": "USD", "period": {"from": "...", "to": "..."},
  "columns": [{"key": "category", "label": "Category", "kind": "text"}, ...],
  "rows": [{...}], "totals": {...}, "notes": ["..."] }
```

With `format=csv` you get a download instead. Money columns are labelled with the currency, and text cells that start with `= + - @` are prefixed with `'` so spreadsheets don't execute them.

## Integrations and sync

| | |
|---|---|
| `GET /integrations/quickbooks` | [integrations.view] `{available_mode, simulated, connection, recent_jobs}` |
| `POST /integrations/quickbooks/connect` | [integrations.manage]. Simulated mode connects at once; live mode returns `{authorize_url}`. |
| `POST /integrations/quickbooks/disconnect` | revokes and deletes the tokens |
| `GET /api/integrations/quickbooks/callback` | OAuth redirect target (not under `/orgs`) |
| `GET /sync-jobs?status` | [integrations.view] |
| `POST /sync-jobs` `{kind}` | [sync.run]. `kind`: `full`, `customers`, `invoices`, `payments`. An optional `Idempotency-Key` header is supported. 202 for a new job; 200 with `existing: true` when one is already active. |
| `GET /sync-jobs/{id}` | `{job, attempts[]}` |
| `POST /sync-jobs/{id}/retry` | [sync.run]. Failed jobs only. |
| `GET /external-records?entity_type` | [integrations.view]. Imported records as stored. |
| `POST /api/webhooks/quickbooks` | Intuit notifications, HMAC-verified (not under `/orgs`, no cookie) |

## Reconciliation

| | |
|---|---|
| `GET /reconciliation?review_status&match_status&entity_type` | [reconciliation.view]. `{data, total, summary, last_run}`. Each record lists its `allowed_decisions`. |
| `POST /reconciliation/run` | [reconciliation.review] |
| `GET /reconciliation/{id}` | `{record, history[], external_payload}` |
| `POST /reconciliation/{id}/review` `{decision, note}` | `decision`: `accepted`, `follow_up`, `ignored`, `imported` (as allowed). The note is required except for `imported`. |

## Audit log

`GET /audit-logs?entity_type&entity_id&action&actor_user_id&from&to&cursor` [audit.view]

Returns 50 entries per page, newest first, as `{data: [{action, entity_type, entity_id, actor_type, actor_name, changes: {before, after, …}, request_id, ip, created_at}], next_cursor}`. Pass `next_cursor` back as `cursor` to get the next page.
