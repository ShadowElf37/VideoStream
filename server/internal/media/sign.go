package media

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// Signed media URLs.
//
// A <video src> cannot send an Authorization header, so the session-bearer
// scheme the JSON API uses does not reach media at all. Cookies would work but
// would have to be scoped, SameSite'd and cleaned up; a signature in the URL
// is simpler and expires on its own.
//
// The signature covers the title and the expiry, so a URL cannot be edited
// into a different title or a later deadline. It is minted by the server
// inside the playback state a client already receives, so a client never
// constructs one and never sees the key.
//
// It covers the title rather than the individual file because a title is the
// unit a viewer is given: the HLS playlists reference their own renditions and
// their own byte ranges, and a player following them sends no headers we
// control, so one signature has to travel to every file in the directory. The
// alternative — re-signing each URI inside a generated playlist — buys nothing,
// since holding any of them already means holding the whole title.
//
// The URL is a capability: anyone holding it can fetch that file until it
// expires. That is the same property the invite link already has, and the
// expiry bounds it.

// ErrBadSignature covers every rejection: wrong signature, malformed, expired.
// One error because the caller should say the same thing to all of them.
var ErrBadSignature = errors.New("bad or expired media signature")

// DefaultTTL is how long a media URL stays valid. Long enough to watch a
// three-hour film without the src going stale mid-playback (a browser
// re-requests ranges throughout, and a 403 halfway through is a dead player),
// short enough that a leaked URL is not forever.
const DefaultTTL = 6 * time.Hour

// Sign returns the query string ("e=…&s=…") authorising a title until now+ttl.
func Sign(secret []byte, id string, ttl time.Duration) string {
	exp := time.Now().Add(ttl).Unix()
	return fmt.Sprintf("e=%d&s=%s", exp, signature(secret, id, exp))
}

// URL returns a complete path plus query for one file in a title.
func URL(secret []byte, id, file string, ttl time.Duration) string {
	return "/media/" + url.PathEscape(id) + "/" + url.PathEscape(file) + "?" + Sign(secret, id, ttl)
}

// Verify checks a signature against the title it claims to authorise.
func Verify(secret []byte, id, expStr, sig string) error {
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return ErrBadSignature
	}
	// Compare before checking expiry so a wrong signature and an expired one
	// take the same path.
	want := signature(secret, id, exp)
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return ErrBadSignature
	}
	if time.Now().Unix() > exp {
		return ErrBadSignature
	}
	return nil
}

func signature(secret []byte, id string, exp int64) string {
	mac := hmac.New(sha256.New, secret)
	// Length-prefixed rather than concatenated, so a title id that happens to
	// contain the separator cannot be made to sign as another.
	fmt.Fprintf(mac, "%d:%s|%d", len(id), id, exp)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
