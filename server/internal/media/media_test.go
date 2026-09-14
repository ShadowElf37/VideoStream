package media

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ShadowElf37/VideoStream/proto"
)

var secret = []byte("test-secret-at-least-32-bytes-ok")

// A signed URL is a capability handed to a browser, so the ways it can be
// abused are the ways this must fail.
func TestVerifyRejectsTampering(t *testing.T) {
	const id = "madoka_04"
	q := Sign(secret, id, time.Hour)
	exp, sig := parseQuery(t, q)

	if err := Verify(secret, id, exp, sig); err != nil {
		t.Fatalf("a freshly signed URL was rejected: %v", err)
	}

	for _, tc := range []struct {
		name     string
		id, e, s string
	}{
		{"different title", "other_title", exp, sig},
		{"extended expiry", id, "99999999999", sig},
		{"forged signature", id, exp, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		{"empty signature", id, exp, ""},
		{"empty expiry", id, "", sig},
		{"non-numeric expiry", id, "soon", sig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Verify(secret, tc.id, tc.e, tc.s); err == nil {
				t.Error("accepted")
			}
		})
	}

	// A different key must not validate.
	if err := Verify([]byte("a completely different secret!!!"), id, exp, sig); err == nil {
		t.Error("a signature from another key was accepted")
	}
}

// One signature covers a whole title, because the HLS playlists reference
// their own renditions and a player following them sends no headers we
// control. Holding any URI in a title already means holding the title.
func TestSignatureCoversEveryFileInTheTitle(t *testing.T) {
	q := Sign(secret, "madoka_04", time.Hour)
	exp, sig := parseQuery(t, q)
	for _, file := range []string{proto.MovieFileName, "movie.720p.mp4", proto.MasterPlaylistName, "720p.m3u8"} {
		if !ValidFile(file) && !strings.HasSuffix(file, ".m3u8") {
			t.Errorf("%s is not servable at all", file)
		}
		if err := Verify(secret, "madoka_04", exp, sig); err != nil {
			t.Errorf("the title signature did not authorise %s: %v", file, err)
		}
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	q := Sign(secret, "x", -time.Second)
	exp, sig := parseQuery(t, q)
	if err := Verify(secret, "x", exp, sig); err == nil {
		t.Error("an expired signature was accepted")
	}
}

// Length-prefixing the signed fields is what stops a title id that contains
// the separator from signing as another one.
func TestSignatureIsUnambiguous(t *testing.T) {
	if Sign(secret, "ab|1", time.Hour) == Sign(secret, "ab", time.Hour) {
		t.Error("two different ids produced the same signature")
	}
}

// Ids reach the filesystem, so this is the boundary that makes traversal a
// 404 instead of a file read.
func TestValidID(t *testing.T) {
	ok := []string{"madoka_04", "a", "Movie.2019", "x-y_z.1"}
	bad := []string{
		"", ".", "..", "../etc", "a/b", "a\\b", ".hidden",
		"a b", "a;b", "a\x00b", "café", "%2e%2e",
	}
	for _, id := range ok {
		if !ValidID(id) {
			t.Errorf("ValidID(%q) = false, want true", id)
		}
	}
	for _, id := range bad {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true, want false", id)
		}
	}
}

func TestValidFile(t *testing.T) {
	if !ValidFile(proto.MovieFileName) || !ValidFile(proto.MetaFileName) {
		t.Error("the two servable names were rejected")
	}
	for _, f := range []string{"", "other.mp4", "../meta.json", "movie.mp4.bak"} {
		if ValidFile(f) {
			t.Errorf("ValidFile(%q) = true, want false", f)
		}
	}
}

func TestPathRefusesEscape(t *testing.T) {
	l := New(t.TempDir())
	for _, tc := range [][2]string{
		{"../../etc", proto.MovieFileName},
		{"ok", "../../../etc/passwd"},
		{"ok", "passwd"},
	} {
		if _, err := l.Path(tc[0], tc[1]); err == nil {
			t.Errorf("Path(%q, %q) was allowed", tc[0], tc[1])
		}
	}
}

func TestListSkipsUnreadableTitles(t *testing.T) {
	root := t.TempDir()
	l := New(root)

	write := func(id string, m proto.MediaMeta) {
		t.Helper()
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		blob, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(dir, proto.MetaFileName), blob, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("b_title", proto.MediaMeta{Title: "B", PushedAt: "2026-01-02T00:00:00Z"})
	write("a_title", proto.MediaMeta{Title: "A", PushedAt: "2026-01-03T00:00:00Z"})

	// A push interrupted between the movie and its metadata must not take the
	// whole listing down with it.
	if err := os.MkdirAll(filepath.Join(root, "half_pushed"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Nor should a corrupt one.
	write("corrupt", proto.MediaMeta{})
	if err := os.WriteFile(filepath.Join(root, "corrupt", proto.MetaFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := l.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("listed %d titles, want 2: %+v", len(got), got)
	}
	// Newest push first.
	if got[0].ID != "a_title" || got[1].ID != "b_title" {
		t.Errorf("order = %s, %s; want a_title, b_title", got[0].ID, got[1].ID)
	}
}

// A library directory that does not exist yet is a normal state, not an error:
// it starts working the moment something is pushed.
func TestListOnMissingRoot(t *testing.T) {
	got, err := New(filepath.Join(t.TempDir(), "nope")).List()
	if err != nil {
		t.Fatalf("List on a missing root: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d titles", len(got))
	}
}

func parseQuery(t *testing.T, q string) (exp, sig string) {
	t.Helper()
	var e, s string
	for _, kv := range splitAmp(q) {
		switch {
		case len(kv) > 2 && kv[:2] == "e=":
			e = kv[2:]
		case len(kv) > 2 && kv[:2] == "s=":
			s = kv[2:]
		}
	}
	if e == "" || s == "" {
		t.Fatalf("Sign returned %q, which has no e= and s=", q)
	}
	return e, s
}

func splitAmp(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '&' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
