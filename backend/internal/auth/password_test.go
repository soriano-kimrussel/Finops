package auth

import (
	"strings"
	"testing"
	"time"
)

func TestHashAndVerifyPassword(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$") {
		t.Fatalf("unexpected encoding %q", h)
	}
	if ok, err := VerifyPassword("correct horse battery staple", h); err != nil || !ok {
		t.Fatalf("expected match, got %v %v", ok, err)
	}
	if ok, _ := VerifyPassword("wrong password entirely", h); ok {
		t.Fatal("wrong password must not verify")
	}
	h2, _ := HashPassword("correct horse battery staple")
	if h == h2 {
		t.Fatal("hashes must be salted")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	if _, err := VerifyPassword("x", "$bcrypt$whatever"); err == nil {
		t.Fatal("expected error for unsupported hash")
	}
}

func TestSessionTokenHashing(t *testing.T) {
	raw, hash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 40 || len(hash) != 32 {
		t.Fatalf("unexpected token sizes %d %d", len(raw), len(hash))
	}
	if string(HashToken(raw)) != string(hash) {
		t.Fatal("hash must be deterministic")
	}
}

func TestAttemptLimiter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := newAttemptLimiter(3, time.Minute)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if !l.Allow("k") {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	if l.Allow("k") {
		t.Fatal("4th attempt should be blocked")
	}
	now = now.Add(2 * time.Minute)
	if !l.Allow("k") {
		t.Fatal("window should reset")
	}
}

func TestValidEmail(t *testing.T) {
	for _, ok := range []string{"a@b.co", "first.last@example.com"} {
		if !ValidEmail(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "nope", "a@b", "Name <a@b.co>", "a@b.co "} {
		if ValidEmail(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
