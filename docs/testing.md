# Testing

```powershell
powershell -ExecutionPolicy Bypass -File scripts\test-all.ps1        # everything except the browser tests
powershell -ExecutionPolicy Bypass -File scripts\test-all.ps1 -E2E   # plus Playwright
```

Everything runs natively on Windows against a local PostgreSQL, with no containers. Last full run: all Go packages pass, 24 frontend tests pass, and 4 Playwright tests covering 11 scenarios pass.

## Backend (Go)

```powershell
cd backend
go test -p 2 ./...
```

Tests that touch the database use `internal/testdb`:
- The first test builds a **template database** with every migration applied. Its name includes a hash of the migration files, so it is rebuilt automatically whenever a migration changes.
- Every test then gets its own `CREATE DATABASE … TEMPLATE …` copy, dropped when it finishes. Tests never share state and never touch your dev data.
- Tests connect both as `finops_app` (the real runtime role, RLS enforced) and as `finops_owner` (for fixtures and for asserting what is really stored).

`-p 2` limits how many packages run at once. PostgreSQL on Windows occasionally drops a freshly accepted connection under heavy churn (`getsockname() failed` in its log), so the helpers also retry connection setup briefly.

Integration tests in `internal/server` drive the real router through an in-memory `http.RoundTripper`, using the same handlers and middleware as production, with a cookie jar. No socket is opened, which keeps them fast and avoids a Windows loopback-socket hang we hit with `httptest.Server`.

What is covered:

| Area | Tests |
|---|---|
| Money rules | rounding half away from zero, JPY and KWD precision, `0.1 + 0.2`, strict parsing (exponents, separators, too many decimals) |
| Invoice maths | line rounding, subtotal, percent and fixed discounts, discount above subtotal, tax rounded once, zero-decimal currency, the total identity |
| Invoices (API) | server-calculated totals and preview, gapless numbering, validation errors per line, the edit/send/void lifecycle, optimistic locking, locking after payment |
| Payments | partial, full, overpayment refused, paying a draft or paid invoice refused, **duplicate request replayed**, idempotency key reused for different content, **10 concurrent payments never overpay**, **8 concurrent retries with one key create one payment**, reversal restores the balance and the ledger nets to zero |
| Overdue | derived overdue status and filter; paid invoices are never overdue |
| Rollback | a failure after writing leaves no payment and an unchanged invoice; a failed create doesn't consume an invoice number |
| Database guards | raw SQL as the app role cannot edit payments, paid invoices, locked line items, the ledger or audit rows, overpay, or break status consistency |
| Budgets | thresholds (80%, 100%, over), allocation to category lines, the general line and unbudgeted spend; end to end through approvals |
| Expenses | approval permissions, no self-approval, rejection needs a note, approved expenses locked |
| Auth | argon2id hashing, session token hashing, hardened cookie, login limiter, register validation, identical response for unknown email and wrong password |
| Authorization | the role matrix; viewers and project managers refused on financial writes; owner-role rules; last owner protected |
| Tenant isolation | 404 for other organizations' URLs and ids, RLS hides rows from raw queries, cross-tenant insert refused, tenant setting doesn't leak across pooled connections, the runtime refuses roles that bypass RLS |
| Reports | dashboard figures from known books, the period filter, other currencies excluded and counted, P&L, AR aging buckets, outstanding list, cash flow, expense shares, CSV output and formula-injection escaping, range validation |
| QuickBooks client | exact decimal decoding, paging query, `Retry-After`, persistent throttling becomes retryable, 5xx retries, 401, API faults, record validation, webhook HMAC, simulated provider labelling and determinism |
| Worker | full simulated sync and idempotent re-sync, transient failure → backoff → success, permanent failure → manual retry, **6 workers × 4 jobs, each claimed once**, expired lease reclaimed and the stale worker can't finish, live token refresh and rotation stored encrypted, revoked refresh marks `needs_reauth`, webhook enqueues once per delivery |
| Reconciliation | matching rules (unit), decisions and their constraints, import creates an invoice that later matches, history and supersede, decisions immutable in SQL |
| Audit | entries written for each change, append-only even for the owner, permissions, cursor paging |
| Migrations | the expand → backfill → enforce → contract sequence, refusal before backfill, idempotent backfill, rollback restoring data |
| Demo seed | the generated books are internally consistent (paid amounts equal payments, the ledger matches payments minus approved expenses, no dates in the future) |

## Frontend (Vitest)

```powershell
cd frontend
npm test
```

Component tests render the real router with a memory history, and mock the API with MSW:
- login validation and server errors, redirects for anonymous users and non-members, navigation filtered by role
- the invoice editor shows the server's totals and sends what the user typed, client and server validation errors
- the payment dialog reuses one idempotency key across a retry and shows the overpayment error on the field; viewers see no payment actions
- money and date formatting (no float drift, JPY and KWD, calendar dates without time-zone shifts), budget meter and warning states
- recording a reconciliation decision

## End to end (Playwright)

```powershell
cd frontend
npx playwright install chromium   # once
npx playwright test
```

`playwright.config.ts` starts everything itself:
- `scripts/e2e-server.ps1` recreates a `finops_e2e` database, builds the API and serves it on port 8081 in simulated QuickBooks mode with the in-process worker
- Vite runs on port 5174, bound to 127.0.0.1

`e2e/workflow.spec.ts` covers the eleven required scenarios:

1. sign up, log out, a failed login, then log in
2. create an organization
3. create a customer
4. create a project
5. create an invoice (totals checked) and mark it sent
6. record a partial payment through the confirmation step
7. check the outstanding balance, and that an overpayment is refused
8. check the dashboard figures and the recent ledger entry
9. connect simulated QuickBooks and run a sync until it succeeds
10. review a reconciliation record and find the decision in the audit log
11. a second user's organization cannot reach the first organization's invoice through the UI or the API

Failures keep a trace and screenshot in `frontend/test-results`.

## Screenshots

`screenshots/capture.spec.ts` regenerates `docs/screenshots` from a running dev stack with demo data:

```powershell
npx playwright test -c playwright.screens.config.ts
```
