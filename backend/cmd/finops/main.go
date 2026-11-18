package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "time/tzdata"

	"finopshq/internal/config"
	"finopshq/internal/database"
	"finopshq/internal/server"
)

const usage = `usage: finops <command>

commands:
  serve                 run the HTTP API
  migrate up            apply all pending migrations
  migrate down N        roll back N migrations (development only)
  migrate version       print the current schema version
  migrate force V       mark version V as clean after a failed migration
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

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server.New(server.Deps{Config: cfg, Pool: pool}),
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
