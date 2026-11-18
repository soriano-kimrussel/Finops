package secrets

import (
	"bytes"
	"testing"
)

func TestSealOpen(t *testing.T) {
	b, err := New(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ct, err := b.Seal([]byte("refresh-token-value"), []byte("org-a"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("refresh-token-value")) {
		t.Fatal("ciphertext must not contain the plaintext")
	}
	pt, err := b.Open(ct, []byte("org-a"))
	if err != nil || string(pt) != "refresh-token-value" {
		t.Fatalf("round trip failed: %q %v", pt, err)
	}
	if _, err := b.Open(ct, []byte("org-b")); err == nil {
		t.Fatal("a token bound to one organization must not decrypt for another")
	}
	ct[len(ct)-1] ^= 1
	if _, err := b.Open(ct, []byte("org-a")); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
	again, _ := b.Seal([]byte("refresh-token-value"), []byte("org-a"))
	if bytes.Equal(again, ct) {
		t.Fatal("nonces must make each ciphertext unique")
	}
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("short keys must be rejected")
	}
}
