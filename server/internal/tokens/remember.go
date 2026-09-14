package tokens

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// Remember is the payload of the vs_auth cookie: the role a device has
// earned by getting through the door once. It is long-lived on purpose — a
// host types the password once per device, and a friend who has been in
// before keeps getting in after the link they were sent has rotated.
type Remember struct {
	Role     string `json:"role"`
	IssuedAt int64  `json:"iat"` // unix seconds
}

// rememberPrefix marks the token as a cookie, and the MAC is taken over the
// prefixed body, so a cookie can never be presented as a session token or
// the other way round even though both use the same secret.
const rememberPrefix = "r."

// SignRemember encodes and signs a Remember as "r." + base64url(json) + "." + base64url(hmac).
func SignRemember(secret []byte, r Remember) (string, error) {
	if r.IssuedAt == 0 {
		r.IssuedAt = time.Now().Unix()
	}
	body, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	encBody := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(rememberPrefix + encBody))
	return rememberPrefix + encBody + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// VerifyRemember checks a cookie value's shape and signature and returns its
// payload. Cookies do not expire server-side; the browser's Max-Age does that.
func VerifyRemember(secret []byte, token string) (*Remember, error) {
	if !strings.HasPrefix(token, rememberPrefix) {
		return nil, ErrInvalidSession
	}
	rest := strings.TrimPrefix(token, rememberPrefix)
	parts := strings.SplitN(rest, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, ErrInvalidSession
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(rememberPrefix + parts[0]))
	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(mac.Sum(nil), got) {
		return nil, ErrInvalidSession
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidSession
	}
	var r Remember
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, ErrInvalidSession
	}
	return &r, nil
}
