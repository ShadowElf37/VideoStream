package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/ShadowElf37/VideoStream/server/internal/media"
	"github.com/ShadowElf37/VideoStream/server/internal/media/mediatest"
)

// writeLibrary lays out a title on disk with recognisable bytes so a range
// response can be checked against the offsets it claims.
func writeLibrary(t *testing.T, srv *Server) (root string, body []byte) {
	t.Helper()
	root = t.TempDir()
	dir := filepath.Join(root, "test_title")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body = make([]byte, 100_000)
	for i := range body {
		body[i] = byte(i % 251)
	}
	if err := os.WriteFile(filepath.Join(dir, proto.MovieFileName), body, 0o644); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(proto.MediaMeta{
		ID: "test_title", Title: "Test Title", DurationMS: 60000,
		Width: 1920, Height: 1080, FPSNum: 24000, FPSDen: 1001,
		VideoCodec: "h264", AudioCodec: "aac", PushedAt: "2026-01-01T00:00:00Z",
	})
	if err := os.WriteFile(filepath.Join(dir, proto.MetaFileName), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	srv.cfg.MediaRoot = root
	srv.library = media.New(root)
	return root, body
}

// Range support is the whole contract a seeking <video> depends on: without a
// correct 206 the browser cannot jump anywhere without refetching the file.
func TestMediaFileServesRanges(t *testing.T) {
	handler, _, srv := newTestServer(t)
	_, body := writeLibrary(t, srv)

	url := media.URL(srv.cfg.SessionSecret, "test_title", proto.MovieFileName, time.Hour)

	t.Run("whole file", func(t *testing.T) {
		rec := doRequest(t, handler, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		if !bytes.Equal(rec.Body.Bytes(), body) {
			t.Error("body differs from the file on disk")
		}
		if got := rec.Header().Get("Content-Type"); got != "video/mp4" {
			t.Errorf("Content-Type = %q, want video/mp4", got)
		}
		if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
			t.Errorf("Accept-Ranges = %q; a browser needs this to seek", got)
		}
	})

	t.Run("byte range", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Range", "bytes=1000-1099")
		rec := doRequest(t, handler, req)
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("status %d, want 206", rec.Code)
		}
		if !bytes.Equal(rec.Body.Bytes(), body[1000:1100]) {
			t.Error("the returned bytes are not the ones asked for")
		}
		if got, want := rec.Header().Get("Content-Range"), fmt.Sprintf("bytes 1000-1099/%d", len(body)); got != want {
			t.Errorf("Content-Range = %q, want %q", got, want)
		}
	})

	t.Run("open-ended range", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Range", "bytes=99000-")
		rec := doRequest(t, handler, req)
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("status %d, want 206", rec.Code)
		}
		if !bytes.Equal(rec.Body.Bytes(), body[99000:]) {
			t.Error("tail range mismatch")
		}
	})

	t.Run("unsatisfiable range", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Range", "bytes=999999-")
		if rec := doRequest(t, handler, req); rec.Code != http.StatusRequestedRangeNotSatisfiable {
			t.Errorf("status %d, want 416", rec.Code)
		}
	})
}

func TestMediaFileRequiresAValidSignature(t *testing.T) {
	handler, _, srv := newTestServer(t)
	writeLibrary(t, srv)

	good := media.URL(srv.cfg.SessionSecret, "test_title", proto.MovieFileName, time.Hour)
	expired := media.URL(srv.cfg.SessionSecret, "test_title", proto.MovieFileName, -time.Hour)

	for _, tc := range []struct {
		name string
		url  string
		want int
	}{
		{"valid", good, http.StatusOK},
		{"no signature", "/media/test_title/" + proto.MovieFileName, http.StatusForbidden},
		{"expired", expired, http.StatusForbidden},
		{"signature for another title", "/media/other/" + proto.MovieFileName +
			"?" + media.Sign(srv.cfg.SessionSecret, "test_title", time.Hour), http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := doRequest(t, handler, httptest.NewRequest(http.MethodGet, tc.url, nil)); rec.Code != tc.want {
				t.Errorf("status %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestListMediaSignsURLs(t *testing.T) {
	handler, _, srv := newTestServer(t)
	writeLibrary(t, srv)

	viewer := joinAs(t, handler, proto.RoleViewer)

	rec := doJSON(t, handler, http.MethodGet, "/api/media", nil, viewer.Session)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Items []struct {
			proto.MediaMeta
			URL string `json:"url"`
		} `json:"items"`
		FreeBytes uint64 `json:"freeBytes"`
	}
	decodeBody(t, rec, &out)
	if len(out.Items) != 1 {
		t.Fatalf("listed %d titles, want 1", len(out.Items))
	}
	if out.Items[0].Title != "Test Title" || out.Items[0].DurationMS != 60000 {
		t.Errorf("metadata not carried through: %+v", out.Items[0].MediaMeta)
	}
	// The URL must work as handed out — a client never builds one itself.
	if rec := doRequest(t, handler, httptest.NewRequest(http.MethodGet, out.Items[0].URL, nil)); rec.Code != http.StatusOK {
		t.Errorf("the listed URL did not fetch: status %d", rec.Code)
	}
	if out.FreeBytes == 0 {
		t.Error("FreeBytes = 0; the library view cannot warn about disk")
	}

	// Listing requires a session at all.
	if rec := doJSON(t, handler, http.MethodGet, "/api/media", nil, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated list: status %d, want 401", rec.Code)
	}
}

func TestDeleteMediaIsHostOnly(t *testing.T) {
	handler, _, srv := newTestServer(t)
	root, _ := writeLibrary(t, srv)

	host := joinAs(t, handler, proto.RoleHost)
	viewer := joinAs(t, handler, proto.RoleViewer)

	if rec := doJSON(t, handler, http.MethodDelete, "/api/media/test_title", nil, viewer.Session); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer delete: status %d, want 403", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "test_title")); err != nil {
		t.Fatal("the viewer's refused delete removed it anyway")
	}

	if rec := doJSON(t, handler, http.MethodDelete, "/api/media/test_title", nil, host.Session); rec.Code != http.StatusNoContent {
		t.Fatalf("host delete: status %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "test_title")); !os.IsNotExist(err) {
		t.Error("the title is still on disk after a successful delete")
	}
	if rec := doJSON(t, handler, http.MethodDelete, "/api/media/test_title", nil, host.Session); rec.Code != http.StatusNotFound {
		t.Errorf("deleting it twice: status %d, want 404", rec.Code)
	}
}

// With no library configured the feature must not half-exist.
func TestMediaDisabledWithoutRoot(t *testing.T) {
	handler, _, srv := newTestServer(t)
	srv.library = nil
	srv.cfg.MediaRoot = ""

	viewer := joinAs(t, handler, proto.RoleViewer)

	if rec := doJSON(t, handler, http.MethodGet, "/api/media", nil, viewer.Session); rec.Code != http.StatusNotFound {
		t.Errorf("list: status %d, want 404", rec.Code)
	}
	if rec := doRequest(t, handler, httptest.NewRequest(http.MethodGet, "/media/x/"+proto.MovieFileName, nil)); rec.Code != http.StatusNotFound {
		t.Errorf("file: status %d, want 404", rec.Code)
	}
}

func TestServerTime(t *testing.T) {
	handler, _, _ := newTestServer(t)
	rec := doRequest(t, handler, httptest.NewRequest(http.MethodGet, "/api/time", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var out struct {
		NowMS int64 `json:"nowMs"`
	}
	decodeBody(t, rec, &out)
	if delta := time.Since(time.UnixMilli(out.NowMS)); delta > time.Minute || delta < -time.Minute {
		t.Errorf("nowMs is %v away from this process's clock", delta)
	}
}

// HLS -----------------------------------------------------------------------

// writeFragmentedLibrary lays out a title with two renditions, both real
// fragmented MP4s, so the playlists can be checked against actual byte ranges.
func writeFragmentedLibrary(t *testing.T, srv *Server) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "frag_title")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct {
		name  string
		count int
	}{{proto.MovieFileName, 4}, {"movie.720p.mp4", 4}} {
		if err := os.WriteFile(filepath.Join(dir, f.name), mediatest.FMP4(15360, f.count, 2000, 900), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	meta, _ := json.Marshal(proto.MediaMeta{
		ID: "frag_title", Title: "Fragmented", DurationMS: 8000,
		Width: 1920, Height: 1080, FPSNum: 24000, FPSDen: 1001,
		VideoCodec: "h264", AudioCodec: "aac", PushedAt: "2026-01-01T00:00:00Z",
		Renditions: []proto.Rendition{
			{Name: "1080p", File: proto.MovieFileName, Width: 1920, Height: 1080, Kbps: 5000},
			{Name: "720p", File: "movie.720p.mp4", Width: 1280, Height: 720, Kbps: 3000},
		},
	})
	if err := os.WriteFile(filepath.Join(dir, proto.MetaFileName), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	srv.cfg.MediaRoot = root
	srv.library = media.New(root)
	return root
}

// The whole point of byte-range HLS is that the ranges it publishes are ranges
// of a file that is really there — so this follows the playlist and fetches
// them, which is what a player does.
func TestHLSPlaylistsAddressRealBytes(t *testing.T) {
	handler, _, srv := newTestServer(t)
	writeFragmentedLibrary(t, srv)

	master := media.URL(srv.cfg.SessionSecret, "frag_title", proto.MasterPlaylistName, time.Hour)
	rec := doRequest(t, handler, httptest.NewRequest(http.MethodGet, master, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("master playlist: status %d, body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/vnd.apple.mpegurl" {
		t.Errorf("Content-Type = %q", got)
	}
	body := rec.Body.String()
	for _, want := range []string{"#EXTM3U", "RESOLUTION=1920x1080", "RESOLUTION=1280x720", "1080p.m3u8?", "720p.m3u8?"} {
		if !strings.Contains(body, want) {
			t.Fatalf("master playlist missing %q:\n%s", want, body)
		}
	}

	// Every variant URI the master offers has to be fetchable with the same
	// signature it was handed — a player appends nothing of its own.
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasSuffix(line, ".m3u8") && !strings.Contains(line, ".m3u8?") {
			continue
		}
		rec := doRequest(t, handler, httptest.NewRequest(http.MethodGet, "/media/frag_title/"+line, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("variant %q: status %d", line, rec.Code)
		}
		checkSegmentsFetch(t, handler, rec.Body.String())
	}
}

// checkSegmentsFetch walks a media playlist and fetches the init segment and
// every byte range it names, checking the server returns exactly those bytes.
func checkSegmentsFetch(t *testing.T, handler http.Handler, playlist string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(playlist), "\n")
	fetch := func(uri, byteRange string) []byte {
		t.Helper()
		size, offset, ok := strings.Cut(byteRange, "@")
		if !ok {
			t.Fatalf("malformed BYTERANGE %q", byteRange)
		}
		req := httptest.NewRequest(http.MethodGet, "/media/frag_title/"+uri, nil)
		n, _ := strconv.ParseInt(size, 10, 64)
		o, _ := strconv.ParseInt(offset, 10, 64)
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", o, o+n-1))
		rec := doRequest(t, handler, req)
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("range %s of %s: status %d, want 206", byteRange, uri, rec.Code)
		}
		if int64(rec.Body.Len()) != n {
			t.Fatalf("range %s of %s returned %d bytes, want %d", byteRange, uri, rec.Body.Len(), n)
		}
		return rec.Body.Bytes()
	}

	var segments int
	for i, l := range lines {
		if after, ok := strings.CutPrefix(l, "#EXT-X-MAP:URI="); ok {
			uri, rest, _ := strings.Cut(strings.TrimPrefix(after, `"`), `",BYTERANGE="`)
			init := fetch(uri, strings.TrimSuffix(rest, `"`))
			// The init segment has to start with ftyp or a player has nothing
			// to initialise its decoder from.
			if len(init) < 8 || string(init[4:8]) != "ftyp" {
				t.Fatalf("the init segment does not begin with ftyp")
			}
			continue
		}
		if after, ok := strings.CutPrefix(l, "#EXT-X-BYTERANGE:"); ok {
			seg := fetch(lines[i+1], after)
			// And every segment with a moof, which is what makes it
			// independently decodable.
			if len(seg) < 8 || string(seg[4:8]) != "moof" {
				t.Fatalf("segment %d does not begin with moof", segments)
			}
			segments++
		}
	}
	if segments != 4 {
		t.Fatalf("%d segments fetched, want 4", segments)
	}
}

// A title with no renditions predates fragmentation: there is nothing to
// publish, and the client falls back to the plain file it would have used.
func TestHLSAbsentForUnfragmentedTitles(t *testing.T) {
	handler, _, srv := newTestServer(t)
	writeLibrary(t, srv)

	master := media.URL(srv.cfg.SessionSecret, "test_title", proto.MasterPlaylistName, time.Hour)
	if rec := doRequest(t, handler, httptest.NewRequest(http.MethodGet, master, nil)); rec.Code != http.StatusNotFound {
		t.Errorf("master playlist for an unfragmented title: status %d, want 404", rec.Code)
	}

	// And the URL the client is handed is the file itself, not a playlist.
	meta, err := srv.library.Get("test_title")
	if err != nil {
		t.Fatal(err)
	}
	if url := PlayURL(srv.cfg.SessionSecret, meta); !strings.Contains(url, proto.MovieFileName) {
		t.Errorf("play URL = %q, want the plain movie", url)
	}
}

// A playlist is behind the same signature as the bytes; without one it is as
// good as a directory listing of the library.
func TestHLSPlaylistNeedsASignature(t *testing.T) {
	handler, _, srv := newTestServer(t)
	writeFragmentedLibrary(t, srv)

	for _, path := range []string{"/media/frag_title/index.m3u8", "/media/frag_title/1080p.m3u8"} {
		if rec := doRequest(t, handler, httptest.NewRequest(http.MethodGet, path, nil)); rec.Code != http.StatusForbidden {
			t.Errorf("%s without a signature: status %d, want 403", path, rec.Code)
		}
	}

	// A rendition that does not exist is a 404, not a path to probe with.
	sig := media.Sign(srv.cfg.SessionSecret, "frag_title", time.Hour)
	for _, name := range []string{"2160p", "..", "movie"} {
		url := "/media/frag_title/" + name + ".m3u8?" + sig
		if rec := doRequest(t, handler, httptest.NewRequest(http.MethodGet, url, nil)); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", url, rec.Code)
		}
	}
}
