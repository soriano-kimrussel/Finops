package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "time/tzdata"

	"finopshq/internal/backfill"
	"finopshq/internal/config"
	"finopshq/internal/database"
	"finopshq/internal/qbsync"
	"finopshq/internal/seed"
	"finopshq/internal/server"
	"finopshq/internal/store"
)

const usage = `usage: finops <command>

commands:
  serve                 run the HTTP API (and the sync worker when WORKER_IN_PROCESS=true)
  worker                run the background sync worker on its own
  migrate up            apply all pending migrations
  migrate down N        roll back N migrations (development only)
  migrate version       print the current schema version
  migrate force V       mark version V as clean after a failed migration
  migrate goto V        migrate up or down to exactly version V
  backfill vendors      link expenses to vendors in batches (--batch N, --pause 50ms, --verify)
  seed                  load DEMO data (fictional organizations and users); development only
`

func main() {
	config.LoadDotenv()
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})))

	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch os.Args[1] {
	case "serve":
		err = runServe(ctx, cfg)
	case "migrate":
		err = runMigrate(cfg, os.Args[2:])
	case "seed":
		err = runSeed(ctx, cfg)
	case "worker":
		err = runWorker(ctx, cfg)
	case "backfill":
		err = runBackfill(ctx, cfg, os.Args[2:])
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		slog.Error("command failed", "command", os.Args[1], "err", err)
		os.Exit(1)
	}
}

func runServe(ctx context.Context, cfg config.Config) error {
	if err := cfg.RequireRuntime(); err != nil {
		return err
	}
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := database.AssertRestrictedRole(ctx, pool); err != nil {
		return err
	}
	syncSvc, worker, err := qbsync.Build(cfg, pool)
	if err != nil {
		return err
	}
	worker.AfterSync = server.Reconciler(pool)
	if cfg.WorkerInProcess {
		go worker.Run(ctx)
	}
	go pruneSessions(ctx, pool)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server.New(server.Deps{Config: cfg, Pool: pool, Sync: syncSvc}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", cfg.HTTPAddr, "env", cfg.Env)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		slog.Info("shutting down http server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// pruneSessions deletes expired sessions once an hour.
func pruneSessions(ctx context.Context, pool *pgxpool.Pool) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := store.New(pool).DeleteExpiredSessions(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("prune sessions", "err", err)
		} else if n > 0 {
			slog.Info("pruned expired sessions", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func runBackfill(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 || args[0] != "vendors" {
		return errors.New("usage: finops backfill vendors [--batch N] [--pause DURATION] [--verify]")
	}
	fs := flag.NewFlagSet("backfill vendors", flag.ContinueOnError)
	batch := fs.Int("batch", 1000, "rows per transaction")
	pause := fs.Duration("pause", 50*time.Millisecond, "pause between batches to limit load")
	verify := fs.Bool("verify", false, "only check the result")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if err := cfg.RequireMigrate(); err != nil {
		return err
	}
	pool, err := database.Open(ctx, cfg.MigrateDatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if !*verify {
		st, err := backfill.Vendors(ctx, pool, *batch, *pause, func(s backfill.VendorStats) {
			if s.Batches%10 == 0 {
				fmt.Printf("  %d batches, %d expenses linked, %d vendors created (%s)\n", s.Batches, s.Rows, s.VendorsCreated, s.Elapsed.Round(time.Millisecond))
			}
		})
		if err != nil {
			return err
		}
		fmt.Printf("Backfill finished: %d expenses linked in %d batches, %d vendors created, %s.\n", st.Rows, st.Batches, st.VendorsCreated, st.Elapsed.Round(time.Millisecond))
	}
	c, err := backfill.VerifyVendors(ctx, pool)
	if err != nil {
		return err
	}
	fmt.Printf("Verification: %d expenses, %d without vendor, %d name mismatches, %d cross-tenant links, %d unused vendors.\n",
		c.Expenses, c.MissingVendorID, c.NameMismatches, c.CrossTenant, c.OrphanVendors)
	if !c.OK() {
		return errors.New("verification failed; do not apply the enforce migration yet")
	}
	fmt.Println("OK: safe to apply the enforce migration.")
	return nil
}

func runWorker(ctx context.Context, cfg config.Config) error {
	if err := cfg.RequireRuntime(); err != nil {
		return err
	}
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := database.AssertRestrictedRole(ctx, pool); err != nil {
		return err
	}
	_, worker, err := qbsync.Build(cfg, pool)
	if err != nil {
		return err
	}
	worker.AfterSync = server.Reconciler(pool)
	worker.Concurrency = cfg.WorkerConcurrency
	worker.Run(ctx)
	return nil
}

func runSeed(ctx context.Context, cfg config.Config) error {
	if cfg.Env == "production" {
		return errors.New("refusing to load demo data with APP_ENV=production")
	}
	if err := cfg.RequireRuntime(); err != nil {
		return err
	}
	// The seed uses the restricted runtime role so row-level security applies exactly as it does
	// for the API.
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := database.AssertRestrictedRole(ctx, pool); err != nil {
		return err
	}
	start := time.Now()
	sum, err := seed.New(pool, server.ProvisionOrganization).Run(ctx)
	if errors.Is(err, seed.ErrAlreadySeeded) {
		fmt.Println("Demo data is already loaded. To start over, reset the database with `migrate down` followed by `migrate up`.")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("DEMO DATA loaded in %s: %d organizations, %d customers, %d projects, %d invoices, %d payments, %d expenses.\n",
		time.Since(start).Round(time.Millisecond), sum.Organizations, sum.Customers, sum.Projects, sum.Invoices, sum.Payments, sum.Expenses)
	fmt.Printf("Sign in as owner@demo.finops.test (also admin@, accountant@, pm@, viewer@, client@) with password %s\n", seed.DemoPassword)
	return nil
}

func runMigrate(cfg config.Config, args []string) error {
	if err := cfg.RequireMigrate(); err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("migrate needs a subcommand: up, down N, version, force V")
	}
	m, err := database.NewMigrator(cfg.MigrateDatabaseURL)
	if err != nil {
		return err
	}
	defer m.Close()

	switch args[0] {
	case "up":
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return err
		}
	case "down":
		if cfg.Env == "production" {
			return errors.New("migrate down is disabled in production; write a forward migration instead")
		}
		if len(args) < 2 {
			return errors.New("migrate down needs a step count, e.g. migrate down 1")
		}
		n, err := strconv.Atoi(args[1])
		if err != nil || n < 1 {
			return errors.New("step count must be a positive integer")
		}
		if err := m.Steps(-n); err != nil {
			return err
		}
	case "version":
	case "goto":
		if len(args) < 2 {
			return errors.New("migrate goto needs a version")
		}
		v, err := strconv.ParseUint(args[1], 10, 64)
		if err != nil {
			return err
		}
		cur, _, verr := m.Version()
		if cfg.Env == "production" && (verr != nil || uint(v) < cur) {
			return errors.New("migrating down is disabled in production; write a forward migration instead")
		}
		if err := m.Migrate(uint(v)); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return err
		}
	case "force":
		if len(args) < 2 {
			return errors.New("migrate force needs a version")
		}
		v, err := strconv.Atoi(args[1])
		if err != nil {
			return err
		}
		if err := m.Force(v); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown migrate subcommand %q", args[0])
	}

	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		fmt.Println("schema version: none")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("schema version: %d (dirty=%v)\n", v, dirty)
	return nil
}
