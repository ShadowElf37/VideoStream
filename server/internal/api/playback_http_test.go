package api

import (
	"net/http"
	"testing"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/ShadowElf37/VideoStream/server/internal/playback"
)

// withDirector gives a test server a real director over the on-disk library
// that writeLibrary laid out. NewServer only builds one when MEDIA_ROOT is
// configured, which the base test server deliberately is not.
func withDirector(t *testing.T, srv *Server) {
	t.Helper()
	srv.director = playback.New(nil, &mediaResolver{cfg: srv.cfg, lib: srv.library})
}

// The transport is host-gated except for the pause-ish actions, and "start"
// — leaving someone behind who is still buffering — is not a pause.
func TestStartIsHostOnly(t *testing.T) {
	handler, _, srv := newTestServer(t)
	writeLibrary(t, srv)
	withDirector(t, srv)

	host := joinAs(t, handler, proto.RoleHost)
	viewer := joinAs(t, handler, proto.RoleViewer)

	rec := doJSON(t, handler, http.MethodPost, "/api/room/playback", playbackCommand{Action: "load", MediaID: "test_title"}, host.Session)
	if rec.Code != http.StatusOK {
		t.Fatalf("host load: status %d, body %s", rec.Code, rec.Body.String())
	}

	// anyoneCanPause is on by default, so the viewer can pause — and still
	// must not be able to start a film the room is waiting on.
	if rec := doJSON(t, handler, http.MethodPost, "/api/room/playback", playbackCommand{Action: "pause"}, viewer.Session); rec.Code != http.StatusOK {
		t.Fatalf("viewer pause with anyoneCanPause on: status %d", rec.Code)
	}
	if rec := doJSON(t, handler, http.MethodPost, "/api/room/playback", playbackCommand{Action: "start"}, viewer.Session); rec.Code != http.StatusForbidden {
		t.Errorf("viewer start: status %d, want 403", rec.Code)
	}
	if rec := doJSON(t, handler, http.MethodPost, "/api/room/playback", playbackCommand{Action: "start"}, host.Session); rec.Code != http.StatusOK {
		t.Errorf("host start: status %d, want 200", rec.Code)
	}
}

// The readiness report is what waitForEveryone is built on: anyone in the
// room may file one, and the session is what names them.
func TestPlaybackReadyReports(t *testing.T) {
	handler, _, srv := newTestServer(t)
	writeLibrary(t, srv)
	withDirector(t, srv)

	host := joinAs(t, handler, proto.RoleHost)
	viewer := joinAs(t, handler, proto.RoleViewer)

	if rec := doJSON(t, handler, http.MethodPatch, "/api/room/settings", settingsPatch{WaitForEveryone: boolPtr(true)}, host.Session); rec.Code != http.StatusOK {
		t.Fatalf("enable waitForEveryone: status %d, body %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, handler, http.MethodPost, "/api/room/playback", playbackCommand{Action: "load", MediaID: "test_title"}, host.Session)
	if rec.Code != http.StatusOK {
		t.Fatalf("load: status %d, body %s", rec.Code, rec.Body.String())
	}
	var st proto.PlaybackState
	decodeBody(t, rec, &st)
	if !st.Holding {
		t.Fatal("loading with waitForEveryone on did not hold the room")
	}

	// A viewer who is not ready is named on the card.
	body := proto.PlaybackReady{Gen: st.Gen, BufferedAheadMS: 200, Ready: false}
	if rec := doJSON(t, handler, http.MethodPost, "/api/room/playback/ready", body, viewer.Session); rec.Code != http.StatusNoContent {
		t.Fatalf("ready report: status %d, body %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, handler, http.MethodGet, "/api/room/playback", nil, viewer.Session)
	decodeBody(t, rec, &st)
	if !st.Holding {
		t.Fatal("released while the only client that answered said it was not ready")
	}
	if len(st.WaitingFor) != 1 || st.WaitingFor[0] != "tester-viewer" {
		t.Errorf("waitingFor = %v, want [tester-viewer]", st.WaitingFor)
	}

	// A session is required: a report is advice, but anonymous advice has
	// nobody to name in the card.
	if rec := doJSON(t, handler, http.MethodPost, "/api/room/playback/ready", body, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated ready report: status %d, want 401", rec.Code)
	}
}

func boolPtr(b bool) *bool { return &b }
