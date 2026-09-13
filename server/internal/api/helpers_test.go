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
// whose Session is what the role-gated endpoints authenticate against.
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
