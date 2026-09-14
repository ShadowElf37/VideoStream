package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/ShadowElf37/VideoStream/server/internal/chat"
	"github.com/ShadowElf37/VideoStream/server/internal/config"
	"github.com/ShadowElf37/VideoStream/server/internal/rooms"
	"github.com/ShadowElf37/VideoStream/server/internal/store"
)

// stubBroadcaster records broadcasts instead of talking to a real LiveKit
// server, satisfying the chat.Broadcaster interface.
type stubBroadcaster struct {
	mu    sync.Mutex
	calls []broadcastCall
}

type broadcastCall struct {
	Topic   string
	Payload []byte
}

func (b *stubBroadcaster) Broadcast(_ context.Context, topic string, payload []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, broadcastCall{Topic: topic, Payload: payload})
	return nil
}

// newTestServer returns the handler, the broadcaster its chat writes to, and
// the Server itself, which tests need in order to vary configuration that is
// read at request time rather than at construction.
func newTestServer(t *testing.T) (http.Handler, *stubBroadcaster, *Server) {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := &config.Config{
		Listen:           ":0",
		PublicURL:        "http://localhost:8080",
		LiveKitURL:       "ws://localhost:7880",
		LiveKitAPIURL:    "http://127.0.0.1:1", // unreachable on purpose; EnsureRoom errors are logged, not fatal
		LiveKitAPIKey:    "devkey",
		LiveKitAPISecret: "devsecret",
		SessionSecret:    []byte("test-session-secret"),
		DBPath:           ":memory:",
		RoomPassword:     testPassword,
	}

	broadcaster := &stubBroadcaster{}
	roomsSvc := rooms.NewService(st, cfg)
	chatSvc := chat.NewService(st, broadcaster)
	lkClient := lksdk.NewRoomServiceClient(cfg.LiveKitAPIURL, cfg.LiveKitAPIKey, cfg.LiveKitAPISecret)

	logger := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	srv := NewServer(cfg, roomsSvc, chatSvc, lkClient, logger)

	return srv.Routes(nil), broadcaster, srv
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(bytes.TrimRight(p, "\n")))
	return len(p), nil
}

func doJSON(t *testing.T, handler http.Handler, method, path string, body any, bearer string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
}

func roomInfo(t *testing.T, handler http.Handler, key string, cookies ...*http.Cookie) proto.RoomInfo {
	t.Helper()
	path := "/api/room"
	if key != "" {
		path += "?key=" + key
	}
	rec := doJSON(t, handler, http.MethodGet, path, nil, "", cookies...)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d, body %s", path, rec.Code, rec.Body.String())
	}
	var info proto.RoomInfo
	decodeBody(t, rec, &info)
	return info
}

// The door: a stranger sees nothing, the password makes a host, the link a
// viewer, and both are remembered on the device.
func TestDoor(t *testing.T) {
	handler, _, _ := newTestServer(t)

	if info := roomInfo(t, handler, ""); info.Access != proto.AccessNone || info.Occupants != 0 {
		t.Errorf("stranger sees %+v", info)
	}
	if info := roomInfo(t, handler, "not-a-key"); info.Access != proto.AccessNone {
		t.Errorf("bad key sees %+v", info)
	}

	// No credentials at all.
	if rec := doJSON(t, handler, http.MethodPost, "/api/room/token", proto.TokenRequest{Name: "Nobody"}, ""); rec.Code != http.StatusForbidden {
		t.Errorf("no credentials: status %d, body %s", rec.Code, rec.Body.String())
	}
	// Wrong password.
	if rec := doJSON(t, handler, http.MethodPost, "/api/room/token", proto.TokenRequest{Name: "Guess", Password: "nope"}, ""); rec.Code != http.StatusForbidden {
		t.Errorf("wrong password: status %d, body %s", rec.Code, rec.Body.String())
	}

	// The password makes a host and sets the cookie.
	hostRec := doJSON(t, handler, http.MethodPost, "/api/room/token", proto.TokenRequest{Name: "Host Person", Password: testPassword}, "")
	if hostRec.Code != http.StatusOK {
		t.Fatalf("host token: status %d, body %s", hostRec.Code, hostRec.Body.String())
	}
	var host proto.TokenResponse
	decodeBody(t, hostRec, &host)
	if host.Role != proto.RoleHost || host.Token == "" || host.Session == "" {
		t.Errorf("host token = %+v", host)
	}
	if host.Links.Viewer == "" {
		t.Fatal("host got no viewer link to hand out")
	}
	hostCookie := cookieFrom(hostRec)
	if hostCookie == nil || !hostCookie.HttpOnly || hostCookie.MaxAge < 300*24*3600 {
		t.Fatalf("host cookie = %+v; want a long-lived HttpOnly cookie", hostCookie)
	}

	// The cookie alone is enough from now on, and the door says so.
	if info := roomInfo(t, handler, "", hostCookie); info.Access != proto.AccessHost {
		t.Errorf("host cookie at the door: %+v", info)
	}
	again := doJSON(t, handler, http.MethodPost, "/api/room/token", proto.TokenRequest{Name: "Host Person"}, "", hostCookie)
	if again.Code != http.StatusOK {
		t.Fatalf("host via cookie: status %d, body %s", again.Code, again.Body.String())
	}
	var viaCookie proto.TokenResponse
	decodeBody(t, again, &viaCookie)
	if viaCookie.Role != proto.RoleHost {
		t.Errorf("role via cookie = %q", viaCookie.Role)
	}

	// The link makes a viewer.
	key := keyFromLink(t, host.Links.Viewer)
	if info := roomInfo(t, handler, key); info.Access != proto.AccessViewer {
		t.Errorf("viewer key at the door: %+v", info)
	}
	viewerRec := doJSON(t, handler, http.MethodPost, "/api/room/token", proto.TokenRequest{Name: "Alice", Key: key}, "")
	if viewerRec.Code != http.StatusOK {
		t.Fatalf("viewer token: status %d, body %s", viewerRec.Code, viewerRec.Body.String())
	}
	var viewer proto.TokenResponse
	decodeBody(t, viewerRec, &viewer)
	if viewer.Role != proto.RoleViewer {
		t.Errorf("role = %q, want viewer", viewer.Role)
	}
	viewerCookie := cookieFrom(viewerRec)
	if viewerCookie == nil {
		t.Fatal("viewer was not remembered")
	}
	if info := roomInfo(t, handler, "", viewerCookie); info.Access != proto.AccessViewer {
		t.Errorf("viewer cookie at the door: %+v", info)
	}

	// A host opening a friend's viewer link keeps the host cookie.
	mixed := doJSON(t, handler, http.MethodPost, "/api/room/token", proto.TokenRequest{Name: "Host Person", Key: key}, "", hostCookie)
	var mixedTok proto.TokenResponse
	decodeBody(t, mixed, &mixedTok)
	if mixedTok.Role != proto.RoleHost {
		t.Errorf("host with a viewer link got role %q", mixedTok.Role)
	}
	if c := cookieFrom(mixed); c != nil {
		t.Errorf("host cookie was rewritten (to %q) on a viewer link", c.Value)
	}

	// Name validation still applies to people.
	if rec := doJSON(t, handler, http.MethodPost, "/api/room/token", proto.TokenRequest{Name: "   ", Key: key}, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("blank name: status %d", rec.Code)
	}

	// The projector needs the password, and gets its fixed identity.
	if rec := doJSON(t, handler, http.MethodPost, "/api/room/token", proto.TokenRequest{Key: key, Projector: true}, ""); rec.Code != http.StatusForbidden {
		t.Errorf("projector with a viewer key: status %d", rec.Code)
	}
	projRec := doJSON(t, handler, http.MethodPost, "/api/room/token", proto.TokenRequest{Password: testPassword, Projector: true}, "")
	if projRec.Code != http.StatusOK {
		t.Fatalf("projector token: status %d, body %s", projRec.Code, projRec.Body.String())
	}
	var proj proto.TokenResponse
	decodeBody(t, projRec, &proj)
	if proj.Role != proto.RoleProjector || proj.Identity != "projector" {
		t.Errorf("projector token = %+v", proj)
	}
	if cookieFrom(projRec) != nil {
		t.Error("the projector was given a cookie")
	}

	// Logging out clears the cookie.
	out := doJSON(t, handler, http.MethodPost, "/api/room/logout", nil, "", hostCookie)
	if out.Code != http.StatusNoContent {
		t.Fatalf("logout: status %d", out.Code)
	}
	if c := cookieFrom(out); c == nil || c.MaxAge >= 0 {
		t.Errorf("logout did not clear the cookie: %+v", c)
	}
}

func TestPasswordAttemptsAreThrottled(t *testing.T) {
	handler, _, _ := newTestServer(t)
	var last int
	for i := 0; i < 7; i++ {
		rec := doJSON(t, handler, http.MethodPost, "/api/room/token", proto.TokenRequest{Name: "Guess", Password: "wrong"}, "")
		last = rec.Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("7th guess: status %d, want 429", last)
	}
	// And the throttle does not care whether the guess was right.
	if rec := doJSON(t, handler, http.MethodPost, "/api/room/token", proto.TokenRequest{Name: "Host", Password: testPassword}, ""); rec.Code != http.StatusTooManyRequests {
		t.Errorf("correct password while throttled: status %d, want 429", rec.Code)
	}
}

// Rotation is what makes a leaked link stop working, and the cookie is what
// keeps it from punishing the people who were actually there.
func TestRotateLinks(t *testing.T) {
	handler, broadcaster, _ := newTestServer(t)
	host := joinAs(t, handler, proto.RoleHost)
	oldKey := keyFromLink(t, host.Links.Viewer)
	viewerRec := doJSON(t, handler, http.MethodPost, "/api/room/token", proto.TokenRequest{Name: "Alice", Key: oldKey}, "")
	var viewer proto.TokenResponse
	decodeBody(t, viewerRec, &viewer)
	viewerCookie := cookieFrom(viewerRec)

	if rec := doJSON(t, handler, http.MethodPost, "/api/room/links/rotate", nil, viewer.Session); rec.Code != http.StatusForbidden {
		t.Errorf("viewer rotate: status %d, want 403", rec.Code)
	}
	rec := doJSON(t, handler, http.MethodPost, "/api/room/links/rotate", nil, host.Session)
	if rec.Code != http.StatusOK {
		t.Fatalf("host rotate: status %d, body %s", rec.Code, rec.Body.String())
	}
	var links proto.Links
	decodeBody(t, rec, &links)
	newKey := keyFromLink(t, links.Viewer)
	if newKey == oldKey {
		t.Fatal("rotation kept the key")
	}

	if info := roomInfo(t, handler, oldKey); info.Access != proto.AccessNone {
		t.Errorf("old key still works: %+v", info)
	}
	if info := roomInfo(t, handler, newKey); info.Access != proto.AccessViewer {
		t.Errorf("new key does not work: %+v", info)
	}
	if info := roomInfo(t, handler, oldKey, viewerCookie); info.Access != proto.AccessViewer {
		t.Errorf("a remembered viewer was locked out by rotation: %+v", info)
	}
	if rec := doJSON(t, handler, http.MethodPost, "/api/room/token", proto.TokenRequest{Name: "Bob", Key: oldKey}, ""); rec.Code != http.StatusForbidden {
		t.Errorf("stranger with the old key: status %d, want 403", rec.Code)
	}

	// GET links agrees, and the room heard about it.
	linksRec := doJSON(t, handler, http.MethodGet, "/api/room/links", nil, viewer.Session)
	var seen proto.Links
	decodeBody(t, linksRec, &seen)
	if seen.Viewer != links.Viewer {
		t.Errorf("GET links = %q, rotate returned %q", seen.Viewer, links.Viewer)
	}
	broadcaster.mu.Lock()
	defer broadcaster.mu.Unlock()
	var sawSystem bool
	for _, c := range broadcaster.calls {
		var m proto.ChatMessage
		if c.Topic == proto.TopicChat && json.Unmarshal(c.Payload, &m) == nil && m.Kind == "system" {
			sawSystem = true
		}
	}
	if !sawSystem {
		t.Error("no system line announced the rotation")
	}
}

// The watcher's callback is the automatic version of the same thing.
func TestEmptyRoomRotates(t *testing.T) {
	handler, _, srv := newTestServer(t)
	before := viewerKey(t, handler)
	srv.onRoomEmpty(context.Background())
	if after := viewerKey(t, handler); after == before {
		t.Error("the room emptying did not rotate the link")
	}
}

func TestChatAndSettings(t *testing.T) {
	handler, broadcaster, _ := newTestServer(t)
	host := joinAs(t, handler, proto.RoleHost)
	viewer := joinAs(t, handler, proto.RoleViewer)

	postChatRec := doJSON(t, handler, http.MethodPost, "/api/room/chat", map[string]string{"text": "hello world"}, viewer.Session)
	if postChatRec.Code != http.StatusCreated {
		t.Fatalf("post chat: status %d, body %s", postChatRec.Code, postChatRec.Body.String())
	}
	var posted proto.ChatMessage
	decodeBody(t, postChatRec, &posted)
	if posted.Text != "hello world" || posted.From.Identity != viewer.Identity {
		t.Errorf("posted message = %+v", posted)
	}

	getChatRec := doJSON(t, handler, http.MethodGet, "/api/room/chat", nil, viewer.Session)
	if getChatRec.Code != http.StatusOK {
		t.Fatalf("get chat: status %d, body %s", getChatRec.Code, getChatRec.Body.String())
	}
	var history proto.ChatHistoryResponse
	decodeBody(t, getChatRec, &history)
	if len(history.Messages) != 1 || history.Messages[0].Text != "hello world" {
		t.Errorf("history = %+v", history.Messages)
	}
	if rec := doJSON(t, handler, http.MethodGet, "/api/room/chat", nil, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("chat without session: status %d", rec.Code)
	}

	if rec := doJSON(t, handler, http.MethodPatch, "/api/room/settings", map[string]any{"anyoneCanPause": true}, viewer.Session); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer settings patch: status %d, body %s", rec.Code, rec.Body.String())
	}
	// Patch to the opposite of the default: only an actual change is broadcast.
	toggled := !host.Settings.AnyoneCanPause
	hostPatchRec := doJSON(t, handler, http.MethodPatch, "/api/room/settings", map[string]any{"anyoneCanPause": toggled}, host.Session)
	if hostPatchRec.Code != http.StatusOK {
		t.Fatalf("host settings patch: status %d, body %s", hostPatchRec.Code, hostPatchRec.Body.String())
	}
	var newSettings proto.RoomSettings
	decodeBody(t, hostPatchRec, &newSettings)
	if newSettings.AnyoneCanPause != toggled {
		t.Errorf("anyoneCanPause = %v after patch, want %v", newSettings.AnyoneCanPause, toggled)
	}
	// The change sticks for the next joiner.
	if later := joinAs(t, handler, proto.RoleViewer); later.Settings.AnyoneCanPause != toggled {
		t.Errorf("settings handed to a later joiner = %+v", later.Settings)
	}

	broadcaster.mu.Lock()
	defer broadcaster.mu.Unlock()
	var sawChat, sawSettings, sawSystem bool
	for _, c := range broadcaster.calls {
		switch c.Topic {
		case proto.TopicChat:
			var m proto.ChatMessage
			if err := json.Unmarshal(c.Payload, &m); err == nil {
				if m.Kind == "system" {
					sawSystem = true
				} else {
					sawChat = true
				}
			}
		case proto.TopicSettings:
			sawSettings = true
		}
	}
	if !sawChat || !sawSettings || !sawSystem {
		t.Errorf("broadcasts: chat=%v settings=%v system=%v", sawChat, sawSettings, sawSystem)
	}
}

func TestSessionsExpire(t *testing.T) {
	handler, _, srv := newTestServer(t)
	_ = srv
	viewer := joinAs(t, handler, proto.RoleViewer)
	if rec := doJSON(t, handler, http.MethodGet, "/api/room/links", nil, viewer.Session); rec.Code != http.StatusOK {
		t.Fatalf("fresh session: status %d", rec.Code)
	}
	if rec := doJSON(t, handler, http.MethodGet, "/api/room/links", nil, viewer.Session+"x"); rec.Code != http.StatusUnauthorized {
		t.Errorf("tampered session: status %d", rec.Code)
	}
	_ = time.Second
}

func TestHealthz(t *testing.T) {
	handler, _, _ := newTestServer(t)
	rec := doJSON(t, handler, http.MethodGet, "/healthz", nil, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("healthz: status %d, body %q", rec.Code, rec.Body.String())
	}
}
