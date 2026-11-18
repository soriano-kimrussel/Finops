package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	pgxdecimal "github.com/jackc/pgx-shopspring-decimal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbfs "finopshq/db"
)

// PoolOptions tunes a pool; zero values keep pgx defaults.
type PoolOptions struct {
	MinConns int32
	MaxConns int32
}

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	return OpenWith(ctx, url, PoolOptions{})
}

func OpenWith(ctx context.Context, url string, o PoolOptions) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if o.MaxConns > 0 {
		cfg.MaxConns = o.MaxConns
	}
	if o.MinConns > 0 {
		cfg.MinConns = o.MinConns
	}
	cfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		pgxdecimal.Register(conn.TypeMap())
		return nil
	}
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	err = Retry(ctx, func() error {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return pool.Ping(pingCtx)
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return pool, nil
}

// Retry retries connection establishment a few times. PostgreSQL on Windows occasionally drops
// a freshly accepted connection under heavy churn ("getsockname() failed"); a short retry is
// enough to ride over it at startup.
func Retry(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Duration(attempt+1) * 150 * time.Millisecond):
		}
	}
	return err
}

// AssertRestrictedRole refuses runtime connections that would bypass row-level security:
// superusers, BYPASSRLS roles and the owner of the tenant tables.
func AssertRestrictedRole(ctx context.Context, pool *pgxpool.Pool) error {
	var user string
	var super, bypass, owner bool
	err := pool.QueryRow(ctx, `
		SELECT r.rolname, r.rolsuper, r.rolbypassrls,
		       EXISTS (SELECT 1 FROM pg_tables t WHERE t.schemaname = 'public' AND t.tableowner = r.rolname)
		FROM pg_roles r WHERE r.rolname = current_user`).Scan(&user, &super, &bypass, &owner)
	if err != nil {
		return fmt.Errorf("check database role: %w", err)
	}
	if super || bypass || owner {
		return fmt.Errorf("DATABASE_URL connects as %q, which bypasses row-level security; use the restricted finops_app role", user)
	}
	return nil
}

func NewMigrator(url string) (*migrate.Migrate, error) {
	src, err := iofs.New(dbfs.Migrations, "migrations")
	if err != nil {
		return nil, err
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, toMigrateURL(url))
	if err != nil {
		return nil, fmt.Errorf("open migrator: %w", err)
	}
	return m, nil
}

func MigrateUp(url string) error {
	m, err := NewMigrator(url)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

func toMigrateURL(url string) string {
	for _, prefix := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(url, prefix) {
			return "pgx5://" + strings.TrimPrefix(url, prefix)
		}
	}
	return url
}
