# Windows setup, configuration and deployment

FinOps HQ is developed and tested on Windows 11 with native tools only: no Docker, WSL or virtual machines.

## 1. Install the tools

| Tool | Version | Install |
|---|---|---|
| Go | 1.27 or newer (`go.mod` requires 1.27.4) | `winget install GoLang.Go`, or download the `.msi` or `.zip` from https://go.dev/dl |
| Node.js | 20 or newer (built with 24) | `winget install OpenJS.NodeJS.LTS` |
| PostgreSQL | 15 or newer (built with 18) | `winget install PostgreSQL.PostgreSQL.18`, or the EDB installer from https://www.postgresql.org/download/windows/ |
| Git | any | `winget install Git.Git` |
| sqlc | 1.31 (only if you change SQL queries) | download `sqlc_*_windows_amd64.zip` from https://github.com/sqlc-dev/sqlc/releases and put `sqlc.exe` on your PATH |

Add PostgreSQL's `bin` folder (for example `C:\Program Files\PostgreSQL\18\bin`) to your user PATH so `psql` works, then open a new terminal.

If `winget install GoLang.Go` fails, which can happen without admin rights, unzip the Go archive into `%LOCALAPPDATA%\Programs\go` and add `%LOCALAPPDATA%\Programs\go\bin` to your user PATH.

## 2. Create the databases

```powershell
powershell -ExecutionPolicy Bypass -File scripts\setup-db.ps1
```

It asks for the `postgres` superuser password once, then:
- creates `finops_owner` (owns the schema, can create test databases) and `finops_app` (runtime role, cannot bypass row-level security)
- creates the `finops_dev` and `finops_test` databases
- writes `.env` with generated passwords and a token-encryption key

It is safe to run again. Set `PGHOST`, `PGPORT` or `PGSUPERUSER` first to point it at another server.

**No superuser password?** Run your own PostgreSQL instance on port 5433 under your Windows account:

```powershell
powershell -ExecutionPolicy Bypass -File scripts\local-postgres.ps1 init     # create, start, run setup-db
powershell -ExecutionPolicy Bypass -File scripts\local-postgres.ps1 start    # after a reboot
powershell -ExecutionPolicy Bypass -File scripts\local-postgres.ps1 stop
```

## 3. Run it

```powershell
cd backend
go run ./cmd/finops migrate up
go run ./cmd/finops seed          # optional demo data
go run ./cmd/finops serve         # http://127.0.0.1:8080, sync worker included

cd ..\frontend
npm install
npm run dev                       # http://localhost:5173
```

Sign in as `owner@demo.finops.test` with the password `FinOpsDemo!2026`.

**Troubleshooting:**
- **"Permission denied" connecting to `::1`, or the browser can't reach `localhost:5173`.** Some Windows machines block IPv6 loopback. Use `npx vite --host 127.0.0.1`, browse to http://127.0.0.1:5173, and set `FRONTEND_ORIGIN=http://127.0.0.1:5173`. The database URLs already use `127.0.0.1`.
- **`DATABASE_URL connects as "...", which bypasses row-level security`.** `DATABASE_URL` must use `finops_app`. The owner role is only for `MIGRATE_DATABASE_URL`.
- **Running scripts is disabled.** Use `powershell -ExecutionPolicy Bypass -File …` as shown, or `Set-ExecutionPolicy -Scope CurrentUser RemoteSigned`.

## Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `APP_ENV` | `development` | `development`, `test` or `production`. Production adds `Secure` cookies and refuses `seed`, `migrate down` and backwards `goto`. |
| `HTTP_ADDR` | `127.0.0.1:8080` | listen address |
| `FRONTEND_ORIGIN` | `http://localhost:5173` | allowed `Origin` for state-changing requests; base for the OAuth redirect back |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` (JSON logs to stdout) |
| `DATABASE_URL` | — | runtime connection as `finops_app` (required for `serve`, `worker`, `seed`) |
| `MIGRATE_DATABASE_URL` | — | owner connection for `migrate` and `backfill` |
| `TEST_DATABASE_URL`, `TEST_APP_PASSWORD` | — | test databases (owner URL to a maintenance database; app role password) |
| `TOKEN_ENCRYPTION_KEY` | — | base64 of 32 random bytes; encrypts OAuth tokens (required) |
| `WORKER_IN_PROCESS` | `true` | run the sync worker inside `serve` |
| `WORKER_CONCURRENCY` | `2` | parallel jobs per worker process (1–32) |
| `STATIC_DIR` | empty | if set, `serve` also serves the built frontend from this folder |
| `QUICKBOOKS_*` | `simulated` mode | see [quickbooks-integration.md](quickbooks-integration.md) |

Real environment variables override `.env`. Never commit `.env`; `.gitignore` excludes it.

## Deployment

The application is one Go binary plus static files. A straightforward Windows Server or VM deployment:

1. **Build.**
   ```powershell
   cd backend;  go build -o finops.exe ./cmd/finops
   cd frontend; npm ci; npm run build          # → frontend\dist
   ```
2. **Database.** A managed PostgreSQL 15+ or a Windows install. Run `setup-db.ps1` against it, or create the two roles by hand. The owner needs `BYPASSRLS` so its `SECURITY DEFINER` worker functions can see across tenants; the app role must not have it. Use TLS (`sslmode=require` or `verify-full`) for anything that isn't on localhost.
3. **Configure** `APP_ENV=production`, the two database URLs, a fresh `TOKEN_ENCRYPTION_KEY`, `STATIC_DIR=C:\finops\dist`, `FRONTEND_ORIGIN=https://finops.example.com`, and the QuickBooks production credentials with the redirect URL `https://finops.example.com/api/integrations/quickbooks/callback`.
4. **Migrate,** after taking a backup (see [database-migrations.md](database-migrations.md)): `finops.exe migrate up`.
5. **Run** `finops.exe serve` as a Windows service, for example with NSSM or `sc.exe create` using a wrapper. For more throughput, set `WORKER_IN_PROCESS=false` on the API instances and run `finops.exe worker` as a separate service. Workers coordinate through the database, so any number can run.
6. **Front it with TLS.** Use IIS (with ARR as a reverse proxy) or another proxy that terminates HTTPS and forwards to `HTTP_ADDR`. Session cookies are `Secure` in production, so the site must be served over HTTPS.
7. **Health checks:** `/healthz` (liveness) and `/readyz` (database reachable).

Things to add before real multi-instance production use:
- move the in-memory login rate limiter to a shared store
- ship JSON logs to a log store
- schedule backups and test restoring them
- monitor `sync_jobs` with `status = 'failed'` and the reconciliation queue
