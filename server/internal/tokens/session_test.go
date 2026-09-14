package tokens

import (
	"strings"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	secret := []byte("test-secret")
	sess := Session{
		Identity: "alice-ab12",
		Name:     "Alice",
		Color:    "#e57373",
		Role:     "viewer",
		Exp:      time.Now().Add(time.Hour).Unix(),
	}

	token, err := Sign(secret, sess)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !strings.Contains(token, ".") {
		t.Fatalf("expected token to contain a '.' separator, got %q", token)
	}

	got, err := Verify(secret, token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if *got != sess {
		t.Fatalf("round-tripped session mismatch: got %+v, want %+v", *got, sess)
	}
}

func TestVerifyTamperedSignatureFails(t *testing.T) {
	secret := []byte("test-secret")
	sess := Session{Identity: "id", Name: "n", Color: "#fff", Role: "viewer", Exp: time.Now().Add(time.Hour).Unix()}
	token, err := Sign(secret, sess)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	parts := strings.SplitN(token, ".", 2)
	tampered := parts[0] + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, err := Verify(secret, tampered); err == nil {
		t.Fatal("expected error verifying tampered signature")
	}
}

func TestVerifyTamperedBodyFails(t *testing.T) {
	secret := []byte("test-secret")
	sess := Session{Identity: "id", Name: "n", Color: "#fff", Role: "viewer", Exp: time.Now().Add(time.Hour).Unix()}
	token, err := Sign(secret, sess)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	parts := strings.SplitN(token, ".", 2)
	tampered := parts[0] + "XX" + "." + parts[1]
	if _, err := Verify(secret, tampered); err == nil {
		t.Fatal("expected error verifying tampered body")
	}
}

func TestVerifyWrongSecretFails(t *testing.T) {
	sess := Session{Identity: "id", Name: "n", Color: "#fff", Role: "viewer", Exp: time.Now().Add(time.Hour).Unix()}
	token, err := Sign([]byte("secret-a"), sess)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := Verify([]byte("secret-b"), token); err == nil {
		t.Fatal("expected error verifying with wrong secret")
	}
}

func TestVerifyExpiredFails(t *testing.T) {
	secret := []byte("test-secret")
	sess := Session{Identity: "id", Name: "n", Color: "#fff", Role: "viewer", Exp: time.Now().Add(-time.Minute).Unix()}
	token, err := Sign(secret, sess)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := Verify(secret, token); err != ErrSessionExpired {
		t.Fatalf("expected ErrSessionExpired, got %v", err)
	}
}

func TestVerifyMalformedFails(t *testing.T) {
	secret := []byte("test-secret")
	cases := []string{"", "no-dot-here", ".", "a.", ".b"}
	for _, c := range cases {
		if _, err := Verify(secret, c); err == nil {
			t.Errorf("expected error for malformed token %q", c)
		}
	}
}
