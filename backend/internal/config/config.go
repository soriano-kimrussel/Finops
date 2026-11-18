package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type QuickBooks struct {
	Mode                 string
	Environment          string
	ClientID             string
	ClientSecret         string
	RedirectURL          string
	WebhookVerifierToken string
}

type Config struct {
	Env                string
	HTTPAddr           string
	FrontendOrigin     string
	LogLevel           slog.Level
	DatabaseURL        string
	MigrateDatabaseURL string
	StaticDir          string
	TokenEncryptionKey []byte
	WorkerInProcess    bool
	WorkerConcurrency  int
	QuickBooks         QuickBooks
}

func (c Config) IsDevelopment() bool { return c.Env == "development" || c.Env == "test" }

// LoadDotenv reads .env from the working directory or its parent. Real environment
// variables always win over file values.
func LoadDotenv() {
	for _, p := range []string{".env", "../.env"} {
		if _, err := os.Stat(p); err == nil {
			_ = godotenv.Load(p)
			return
		}
	}
}

func Load() (Config, error) {
	cfg := Config{
		Env:                get("APP_ENV", "development"),
		HTTPAddr:           get("HTTP_ADDR", "127.0.0.1:8080"),
		FrontendOrigin:     strings.TrimRight(get("FRONTEND_ORIGIN", "http://localhost:5173"), "/"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		MigrateDatabaseURL: os.Getenv("MIGRATE_DATABASE_URL"),
		StaticDir:          os.Getenv("STATIC_DIR"),
		QuickBooks: QuickBooks{
			Mode:                 get("QUICKBOOKS_MODE", "simulated"),
			Environment:          get("QUICKBOOKS_ENVIRONMENT", "sandbox"),
			ClientID:             os.Getenv("QUICKBOOKS_CLIENT_ID"),
			ClientSecret:         os.Getenv("QUICKBOOKS_CLIENT_SECRET"),
			RedirectURL:          os.Getenv("QUICKBOOKS_REDIRECT_URL"),
			WebhookVerifierToken: os.Getenv("QUICKBOOKS_WEBHOOK_VERIFIER_TOKEN"),
		},
	}

	cfg.WorkerInProcess = get("WORKER_IN_PROCESS", "true") == "true"
	if n, err := strconv.Atoi(get("WORKER_CONCURRENCY", "2")); err == nil && n > 0 && n <= 32 {
		cfg.WorkerConcurrency = n
	} else {
		return cfg, errors.New("WORKER_CONCURRENCY must be a number between 1 and 32")
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(get("LOG_LEVEL", "info"))); err != nil {
		return cfg, fmt.Errorf("LOG_LEVEL: %w", err)
	}

	switch cfg.Env {
	case "development", "test", "production":
	default:
		return cfg, fmt.Errorf("APP_ENV must be development, test or production, got %q", cfg.Env)
	}

	if raw := os.Getenv("TOKEN_ENCRYPTION_KEY"); raw != "" {
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return cfg, fmt.Errorf("TOKEN_ENCRYPTION_KEY is not valid base64: %w", err)
		}
		if len(key) != 32 {
			return cfg, fmt.Errorf("TOKEN_ENCRYPTION_KEY must decode to 32 bytes, got %d", len(key))
		}
		cfg.TokenEncryptionKey = key
	}

	switch cfg.QuickBooks.Mode {
	case "simulated":
	case "live":
		if cfg.QuickBooks.ClientID == "" || cfg.QuickBooks.ClientSecret == "" || cfg.QuickBooks.RedirectURL == "" {
			return cfg, errors.New("QUICKBOOKS_MODE=live requires QUICKBOOKS_CLIENT_ID, QUICKBOOKS_CLIENT_SECRET and QUICKBOOKS_REDIRECT_URL")
		}
	default:
		return cfg, fmt.Errorf("QUICKBOOKS_MODE must be simulated or live, got %q", cfg.QuickBooks.Mode)
	}
	if cfg.QuickBooks.Environment != "sandbox" && cfg.QuickBooks.Environment != "production" {
		return cfg, fmt.Errorf("QUICKBOOKS_ENVIRONMENT must be sandbox or production, got %q", cfg.QuickBooks.Environment)
	}

	return cfg, nil
}

func (c Config) RequireRuntime() error {
	var missing []string
	if c.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if len(c.TokenEncryptionKey) == 0 {
		missing = append(missing, "TOKEN_ENCRYPTION_KEY")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required configuration: %s (run scripts/setup-db.ps1)", strings.Join(missing, ", "))
	}
	return nil
}

func (c Config) RequireMigrate() error {
	if c.MigrateDatabaseURL == "" {
		return errors.New("missing required configuration: MIGRATE_DATABASE_URL (run scripts/setup-db.ps1)")
	}
	return nil
}

func get(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
