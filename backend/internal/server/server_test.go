package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"finopshq/internal/config"
	"finopshq/internal/testdb"
)

func TestHealthAndReadiness(t *testing.T) {
	db := testdb.New(t)
	h := New(Deps{Config: config.Config{FrontendOrigin: "http://localhost:5173", TokenEncryptionKey: make([]byte, 32), QuickBooks: config.QuickBooks{Mode: "simulated"}}, Pool: db.App})

	for _, path := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d body %s", path, rec.Code, rec.Body)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["status"] != "ok" {
			t.Fatalf("%s: unexpected body %s", path, rec.Body)
		}
	}
}

func TestCSRFGuardRejectsMissingHeader(t *testing.T) {
	db := testdb.New(t)
	h := New(Deps{Config: config.Config{FrontendOrigin: "http://localhost:5173", TokenEncryptionKey: make([]byte, 32), QuickBooks: config.QuickBooks{Mode: "simulated"}}, Pool: db.App})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/anything", strings.NewReader("{}")))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/anything", strings.NewReader("{}"))
	req.Header.Set("X-Requested-With", "finops")
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for foreign origin, got %d", rec.Code)
	}
}
