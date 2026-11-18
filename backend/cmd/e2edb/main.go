// Command e2edb recreates the finops_e2e database for the Playwright suite and prints the
// connection URLs as KEY=VALUE lines. It only ever touches the database named finops_e2e.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"finopshq/internal/config"
	"finopshq/internal/database"
)

const dbName = "finops_e2e"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2edb:", err)
		os.Exit(1)
	}
}

func run() error {
	config.LoadDotenv()
	admin := os.Getenv("TEST_DATABASE_URL")
	appPw := os.Getenv("TEST_APP_PASSWORD")
	if admin == "" || appPw == "" {
		return fmt.Errorf("TEST_DATABASE_URL and TEST_APP_PASSWORD must be set (run scripts/setup-db.ps1)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)"); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		return err
	}
	owner := withDB(admin, "", "")
	if err := database.MigrateUp(owner); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	fmt.Printf("MIGRATE_DATABASE_URL=%s\n", owner)
	fmt.Printf("DATABASE_URL=%s\n", withDB(admin, "finops_app", appPw))
	return nil
}

func withDB(raw, user, pw string) string {
	u, _ := url.Parse(raw)
	u.Path = "/" + dbName
	if user != "" {
		u.User = url.UserPassword(user, pw)
	}
	return u.String()
}
