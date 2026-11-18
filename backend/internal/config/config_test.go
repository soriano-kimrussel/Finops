package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func setBase(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("QUICKBOOKS_MODE", "simulated")
	t.Setenv("QUICKBOOKS_ENVIRONMENT", "sandbox")
	t.Setenv("TOKEN_ENCRYPTION_KEY", "")
	t.Setenv("LOG_LEVEL", "info")
}

func TestLoadDefaults(t *testing.T) {
	setBase(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr == "" || cfg.FrontendOrigin == "" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestLoadRejectsBadEncryptionKey(t *testing.T) {
	setBase(t)
	t.Setenv("TOKEN_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("too-short")))
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "32 bytes") {
		t.Fatalf("expected 32-byte error, got %v", err)
	}
}

func TestLoadLiveQuickBooksNeedsCredentials(t *testing.T) {
	setBase(t)
	t.Setenv("QUICKBOOKS_MODE", "live")
	t.Setenv("QUICKBOOKS_CLIENT_ID", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for live mode without credentials")
	}
}

func TestRequireRuntime(t *testing.T) {
	if err := (Config{}).RequireRuntime(); err == nil {
		t.Fatal("expected missing config error")
	}
	ok := Config{DatabaseURL: "postgres://x", TokenEncryptionKey: make([]byte, 32)}
	if err := ok.RequireRuntime(); err != nil {
		t.Fatal(err)
	}
}
