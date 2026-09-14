package tokens

import (
	"testing"
	"time"
)

func TestRememberRoundTrip(t *testing.T) {
	secret := []byte("s")
	tok, err := SignRemember(secret, Remember{Role: "host"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := VerifyRemember(secret, tok)
	if err != nil {
		t.Fatalf("VerifyRemember: %v", err)
	}
	if got.Role != "host" || got.IssuedAt == 0 {
		t.Errorf("payload = %+v", got)
	}
	if _, err := VerifyRemember([]byte("other"), tok); err == nil {
		t.Error("verified with the wrong secret")
	}
}

// A cookie and a session share a secret; neither may pass as the other.
func TestRememberAndSessionAreNotInterchangeable(t *testing.T) {
	secret := []byte("s")
	cookie, _ := SignRemember(secret, Remember{Role: "host"})
	if _, err := Verify(secret, cookie); err == nil {
		t.Error("a cookie verified as a session")
	}
	if _, err := Verify(secret, cookie[len(rememberPrefix):]); err == nil {
		t.Error("a cookie with its prefix stripped verified as a session")
	}
	sess, _ := Sign(secret, Session{Identity: "x", Role: "host", Exp: time.Now().Add(time.Hour).Unix()})
	if _, err := VerifyRemember(secret, sess); err == nil {
		t.Error("a session verified as a cookie")
	}
	if _, err := VerifyRemember(secret, rememberPrefix+sess); err == nil {
		t.Error("a session with the cookie prefix verified as a cookie")
	}
}
