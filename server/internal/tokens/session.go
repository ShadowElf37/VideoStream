package tokens

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ErrInvalidSession is returned for malformed or tampered session tokens.
var ErrInvalidSession = errors.New("invalid session")

// ErrSessionExpired is returned when a session's exp has passed.
var ErrSessionExpired = errors.New("session expired")

// Session is the payload bound into an opaque, HMAC-signed session token.
// It proves who a caller is for the six hours after they joined; the
// long-lived cookie (see remember.go) is a separate thing with a separate
// signature and cannot be presented as a session.
type Session struct {
	Identity string `json:"identity"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	Role     string `json:"role"`
	Exp      int64  `json:"exp"` // unix seconds
}

// Sign encodes and signs a session as base64url(json) + "." + base64url(hmac).
func Sign(secret []byte, s Session) (string, error) {
	body, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	encBody := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(encBody))
	sig := mac.Sum(nil)
	encSig := base64.RawURLEncoding.EncodeToString(sig)
	return encBody + "." + encSig, nil
}

// Verify checks the HMAC signature (constant time) and expiry of a session
// token and returns its payload.
func Verify(secret []byte, token string) (*Session, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, ErrInvalidSession
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0]))
	expected := mac.Sum(nil)

	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidSession
	}
	if !hmac.Equal(expected, got) {
		return nil, ErrInvalidSession
	}

	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidSession
	}
	var s Session
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, ErrInvalidSession
	}
	if time.Now().Unix() > s.Exp {
		return nil, ErrSessionExpired
	}
	return &s, nil
}
