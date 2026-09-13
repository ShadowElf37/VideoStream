package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/ShadowElf37/VideoStream/server/internal/media"
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
			"?" + media.Sign(srv.cfg.SessionSecret, "test_title", proto.MovieFileName, time.Hour), http.StatusForbidden},
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

	createRec := doJSON(t, handler, http.MethodPost, "/api/rooms", proto.CreateRoomRequest{Name: "Party"}, "")
	var created proto.CreateRoomResponse
	decodeBody(t, createRec, &created)
	viewer := tokenFor(t, handler, created, proto.RoleViewer)

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

	createRec := doJSON(t, handler, http.MethodPost, "/api/rooms", proto.CreateRoomRequest{Name: "Party"}, "")
	var created proto.CreateRoomResponse
	decodeBody(t, createRec, &created)
	host := tokenFor(t, handler, created, proto.RoleHost)
	viewer := tokenFor(t, handler, created, proto.RoleViewer)

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

	createRec := doJSON(t, handler, http.MethodPost, "/api/rooms", proto.CreateRoomRequest{Name: "Party"}, "")
	var created proto.CreateRoomResponse
	decodeBody(t, createRec, &created)
	viewer := tokenFor(t, handler, created, proto.RoleViewer)

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
