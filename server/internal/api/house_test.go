package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ShadowElf37/VideoStream/proto"
)

func doRequest(t *testing.T, handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// tokenFor joins the room in the given role and returns the token response,
// whose Session is what the host-only endpoints authenticate against.
func tokenFor(t *testing.T, handler http.Handler, room proto.CreateRoomResponse, role string) proto.TokenResponse {
	t.Helper()
	req := proto.TokenRequest{Name: "tester-" + role}
	switch role {
	case proto.RoleHost:
		req.HostSecret = keyFromLink(t, room.HostLink, "h")
	default:
		req.InviteKey = keyFromLink(t, room.InviteLink, "k")
	}
	rec := doJSON(t, handler, http.MethodPost, "/api/rooms/"+room.ID+"/token", req, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("token for %s: status %d, body %s", role, rec.Code, rec.Body.String())
	}
	var out proto.TokenResponse
	decodeBody(t, rec, &out)
	return out
}

func keyFromLink(t *testing.T, link, param string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parsing %q: %v", link, err)
	}
	v := u.Query().Get(param)
	if v == "" {
		t.Fatalf("link %q has no ?%s=", link, param)
	}
	return v
}

// The assignment endpoint hands out a projector key, so the secret guarding it
// is as sensitive as the key itself. These check it cannot be skipped.
func TestHouseAssignmentRequiresSecret(t *testing.T) {
	handler, _, srv := newTestServer(t)
	srv.cfg.HouseSecret = "s3cret"

	for _, tc := range []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong secret", "Bearer nope", http.StatusUnauthorized},
		{"right secret", "Bearer s3cret", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "/api/house/assignment", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := doRequest(t, handler, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// Without a configured secret the feature must not advertise itself, or the
// UI would offer a button that can never work.
func TestHouseDisabledWhenUnconfigured(t *testing.T) {
	handler, _, srv := newTestServer(t)
	srv.cfg.HouseSecret = ""

	req, _ := http.NewRequest(http.MethodGet, "/api/house/assignment", nil)
	req.Header.Set("Authorization", "Bearer anything")
	if rec := doRequest(t, handler, req); rec.Code != http.StatusNotFound {
		t.Errorf("assignment status = %d, want 404 when unconfigured", rec.Code)
	}
}

func TestHouseProjectorAssignmentFlow(t *testing.T) {
	handler, _, srv := newTestServer(t)
	srv.cfg.HouseSecret = "s3cret"

	createRec := doJSON(t, handler, http.MethodPost, "/api/rooms", proto.CreateRoomRequest{Name: "Party"}, "")
	var created proto.CreateRoomResponse
	decodeBody(t, createRec, &created)

	hostTok := tokenFor(t, handler, created, proto.RoleHost)
	viewerTok := tokenFor(t, handler, created, proto.RoleViewer)

	// A viewer must not be able to commandeer the server's projector.
	if rec := doJSON(t, handler, http.MethodPost, "/api/rooms/"+created.ID+"/projector", nil, viewerTok.Session); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer assign: status %d, want 403", rec.Code)
	}

	if rec := doJSON(t, handler, http.MethodPost, "/api/rooms/"+created.ID+"/projector", nil, hostTok.Session); rec.Code != http.StatusOK {
		t.Fatalf("host assign: status %d, body %s", rec.Code, rec.Body.String())
	}

	// The projector should now be told to join this room, with its key.
	req, _ := http.NewRequest(http.MethodGet, "/api/house/assignment", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	var got houseAssignment
	decodeBody(t, doRequest(t, handler, req), &got)
	if got.RoomID != created.ID {
		t.Errorf("assigned room = %q, want %q", got.RoomID, created.ID)
	}
	if got.ProjectorKey == "" {
		t.Error("assignment carried no projector key, so the projector cannot get a token")
	}

	// A second room cannot take it while the first still holds it: there is
	// one machine with one uplink, and silently stealing it mid-film is worse
	// than refusing.
	otherRec := doJSON(t, handler, http.MethodPost, "/api/rooms", proto.CreateRoomRequest{Name: "Other"}, "")
	var other proto.CreateRoomResponse
	decodeBody(t, otherRec, &other)
	otherHost := tokenFor(t, handler, other, proto.RoleHost)
	if rec := doJSON(t, handler, http.MethodPost, "/api/rooms/"+other.ID+"/projector", nil, otherHost.Session); rec.Code != http.StatusConflict {
		t.Errorf("second room assign: status %d, want 409", rec.Code)
	}

	// Releasing it frees it for the other room.
	if rec := doJSON(t, handler, http.MethodDelete, "/api/rooms/"+created.ID+"/projector", nil, hostTok.Session); rec.Code != http.StatusOK {
		t.Fatalf("release: status %d", rec.Code)
	}
	if rec := doJSON(t, handler, http.MethodPost, "/api/rooms/"+other.ID+"/projector", nil, otherHost.Session); rec.Code != http.StatusOK {
		t.Errorf("assign after release: status %d, want 200", rec.Code)
	}
}
