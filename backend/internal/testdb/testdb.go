// Package testdb gives each test a throwaway PostgreSQL database cloned from a migrated
// template. It needs TEST_DATABASE_URL (owner role) and TEST_APP_PASSWORD, which
// scripts/setup-db.ps1 writes into .env.
package testdb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	dbfs "finopshq/db"
	"finopshq/internal/database"
)

type DB struct {
	Name   string
	Owner  *pgxpool.Pool // finops_owner: bypasses RLS, use for fixtures and assertions
	App    *pgxpool.Pool // finops_app: subject to RLS, what the application uses
	AppURL string
}

func New(t testing.TB) *DB {
	t.Helper()
	loadEnv()
	adminURL := os.Getenv("TEST_DATABASE_URL")
	appPassword := os.Getenv("TEST_APP_PASSWORD")
	if adminURL == "" || appPassword == "" {
		t.Fatal("TEST_DATABASE_URL and TEST_APP_PASSWORD must be set; run scripts/setup-db.ps1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var admin *pgx.Conn
	err := database.Retry(ctx, func() error {
		var err error
		admin, err = pgx.Connect(ctx, adminURL)
		return err
	})
	if err != nil {
		t.Fatalf("connect to test server: %v", err)
	}
	defer admin.Close(ctx)

	tpl, err := ensureTemplate(ctx, admin, adminURL)
	if err != nil {
		t.Fatalf("prepare template database: %v", err)
	}

	name := "finops_t_" + randomHex(6)
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, tpl)); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	ownerURL := withDatabase(adminURL, name, "", "")
	appURL := withDatabase(adminURL, name, "finops_app", appPassword)

	owner, err := database.OpenWith(ctx, ownerURL, database.PoolOptions{MinConns: 1, MaxConns: 4})
	if err != nil {
		t.Fatalf("open owner pool: %v", err)
	}
	app, err := database.OpenWith(ctx, appURL, database.PoolOptions{MinConns: 4, MaxConns: 8})
	if err != nil {
		owner.Close()
		t.Fatalf("open app pool: %v", err)
	}

	t.Cleanup(func() {
		app.Close()
		owner.Close()
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		var conn *pgx.Conn
		err := database.Retry(cctx, func() error {
			var err error
			conn, err = pgx.Connect(cctx, adminURL)
			return err
		})
		if err != nil {
			t.Logf("cleanup connect: %v", err)
			return
		}
		defer conn.Close(cctx)
		if _, err := conn.Exec(cctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name)); err != nil {
			t.Logf("drop test database %s: %v", name, err)
		}
	})

	return &DB{Name: name, Owner: owner, App: app, AppURL: appURL}
}

// NewEmpty creates a throwaway database with no migrations applied and returns its owner URL.
// Migration tests use it to step through schema versions one at a time.
func NewEmpty(t testing.TB) string {
	t.Helper()
	loadEnv()
	adminURL := os.Getenv("TEST_DATABASE_URL")
	if adminURL == "" {
		t.Fatal("TEST_DATABASE_URL must be set; run scripts/setup-db.ps1")
	}
	ctx := context.Background()
	var admin *pgx.Conn
	if err := database.Retry(ctx, func() error {
		var err error
		admin, err = pgx.Connect(ctx, adminURL)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := "finops_m_" + randomHex(6)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn, err := pgx.Connect(context.Background(), adminURL)
		if err != nil {
			return
		}
		defer conn.Close(context.Background())
		_, _ = conn.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	return withDatabase(adminURL, name, "", "")
}

func ensureTemplate(ctx context.Context, admin *pgx.Conn, adminURL string) (string, error) {
	hash, err := migrationsHash()
	if err != nil {
		return "", err
	}
	tpl := "finops_tpl_" + hash

	if _, err := admin.Exec(ctx, "SELECT pg_advisory_lock(hashtext('finops_test_template'))"); err != nil {
		return "", err
	}
	defer admin.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtext('finops_test_template'))")

	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", tpl).Scan(&exists); err != nil {
		return "", err
	}
	if exists {
		return tpl, nil
	}

	rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'finops\\_tpl\\_%'")
	if err != nil {
		return "", err
	}
	stale, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", err
	}
	for _, s := range stale {
		_, _ = admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", s))
	}

	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", tpl)); err != nil {
		return "", err
	}
	if err := database.MigrateUp(withDatabase(adminURL, tpl, "", "")); err != nil {
		_, _ = admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", tpl))
		return "", fmt.Errorf("migrate template: %w", err)
	}
	return tpl, nil
}

func migrationsHash() (string, error) {
	var names []string
	err := fs.WalkDir(dbfs.Migrations, "migrations", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			names = append(names, path)
		}
		return err
	})
	if err != nil {
		return "", err
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, err := fs.ReadFile(dbfs.Migrations, n)
		if err != nil {
			return "", err
		}
		h.Write([]byte(n))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

func withDatabase(raw, dbName, user, password string) string {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	u.Path = "/" + dbName
	if user != "" {
		u.User = url.UserPassword(user, password)
	}
	return u.String()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func loadEnv() {
	if os.Getenv("TEST_DATABASE_URL") != "" {
		return
	}
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, ".env")
		if _, err := os.Stat(candidate); err == nil && !strings.Contains(dir, "node_modules") {
			_ = godotenv.Load(candidate)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}
