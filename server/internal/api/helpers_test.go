package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ShadowElf37/VideoStream/proto"
)

// testPassword is the room password newTestServer configures.
const testPassword = "test-password"

func doRequest(t *testing.T, handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// joinAs gets through the door in the given role and returns the token
// response, whose Session is what the role-gated endpoints authenticate
// against. Hosts use the password; viewers use the current link.
func joinAs(t *testing.T, handler http.Handler, role string) proto.TokenResponse {
	t.Helper()
	req := proto.TokenRequest{Name: "tester-" + role}
	switch role {
	case proto.RoleHost:
		req.Password = testPassword
	default:
		req.Key = viewerKey(t, handler)
	}
	rec := doJSON(t, handler, http.MethodPost, "/api/room/token", req, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("token for %s: status %d, body %s", role, rec.Code, rec.Body.String())
	}
	var out proto.TokenResponse
	decodeBody(t, rec, &out)
	return out
}

// viewerKey is the key in the current invite link, learned the way a host
// would: by getting in with the password.
func viewerKey(t *testing.T, handler http.Handler) string {
	t.Helper()
	rec := doJSON(t, handler, http.MethodPost, "/api/room/token", proto.TokenRequest{Name: "keyfetch", Password: testPassword}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("host token to learn the viewer key: status %d, body %s", rec.Code, rec.Body.String())
	}
	var out proto.TokenResponse
	decodeBody(t, rec, &out)
	return keyFromLink(t, out.Links.Viewer)
}

func keyFromLink(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parsing %q: %v", link, err)
	}
	v := u.Query().Get("k")
	if v == "" {
		t.Fatalf("link %q has no ?k=", link)
	}
	return v
}

// cookieFrom returns the vs_auth cookie a response set, or nil.
func cookieFrom(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == authCookie {
			return c
		}
	}
	return nil
}
