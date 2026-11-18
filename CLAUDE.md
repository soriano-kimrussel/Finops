# FinOps HQ

This project is independent of the OrderFlow `CLAUDE.md` / `AGENTS.md` in the parent folder. Those rules (Next.js, Supabase, `workspace_id`, `lib/email`) do not apply here.

Stack: Go (chi, pgx, sqlc, golang-migrate) in `backend/`, React + TypeScript + Vite in `frontend/`, PostgreSQL 18 running natively on Windows. See `docs/architecture.md`.

## Rules

1. Never use floats for money. Go uses `shopspring/decimal` via `internal/money`; JSON carries amounts as strings; the frontend never computes authoritative totals (invoice totals come from `POST /invoices/preview`).
2. Every tenant table has `organization_id`, composite FKs `(organization_id, id)` and an RLS policy. Tenant queries run inside `tenant.WithTx`.
3. Schema changes only through new files in `backend/db/migrations`, created with `scripts/new-migration.ps1 <name>` (timestamp versions). Never edit an applied migration.
4. After changing `backend/db/queries/*.sql`, run `sqlc generate` in `backend/` and commit the generated code.
5. No Docker, WSL or Linux-only tooling. Scripts are PowerShell.
6. Never commit secrets. Config comes from environment variables (`.env` locally).
7. Audit every financial, permission and integration change through `internal/audit`.
8. Do not claim the live QuickBooks integration works unless it has been tested with real sandbox credentials.

## Before you finish

`powershell -ExecutionPolicy Bypass -File scripts\test-all.ps1` (add `-E2E` for Playwright).
