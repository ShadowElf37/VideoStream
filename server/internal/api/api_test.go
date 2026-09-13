package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"

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
	RoomID  string
	Topic   string
	Payload []byte
}

func (b *stubBroadcaster) Broadcast(_ context.Context, roomID, topic string, payload []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, broadcastCall{RoomID: roomID, Topic: topic, Payload: payload})
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

func doJSON(t *testing.T, handler http.Handler, method, path string, body any, bearer string) *httptest.ResponseRecorder {
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

func TestFullRoomFlow(t *testing.T) {
	handler, broadcaster, _ := newTestServer(t)

	// Create a room with a password.
	createRec := doJSON(t, handler, http.MethodPost, "/api/rooms", proto.CreateRoomRequest{
		Name:     "Test Room",
		Password: "secret123",
	}, "")
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create room: status %d, body %s", createRec.Code, createRec.Body.String())
	}
	var created proto.CreateRoomResponse
	decodeBody(t, createRec, &created)
	if created.ID == "" {
		t.Fatal("expected non-empty room ID")
	}

	inviteKey := mustQueryParam(t, created.InviteLink, "k")
	hostSecret := mustQueryParam(t, created.HostLink, "h")
	projectorKey := mustQueryParam(t, created.ProjectorLink, "p")

	// GET room info.
	infoRec := doJSON(t, handler, http.MethodGet, "/api/rooms/"+created.ID, nil, "")
	if infoRec.Code != http.StatusOK {
		t.Fatalf("get room: status %d, body %s", infoRec.Code, infoRec.Body.String())
	}
	var info proto.RoomInfo
	decodeBody(t, infoRec, &info)
	if !info.HasPassword {
		t.Error("expected HasPassword=true")
	}
	if info.Settings.MaxPreset != proto.Preset1080pHigh {
		t.Errorf("default maxPreset = %q", info.Settings.MaxPreset)
	}

	// Wrong password on invite token.
	wrongPwRec := doJSON(t, handler, http.MethodPost, "/api/rooms/"+created.ID+"/token", proto.TokenRequest{
		Name: "Alice", InviteKey: inviteKey, Password: "wrong",
	}, "")
	if wrongPwRec.Code != http.StatusForbidden {
		t.Fatalf("wrong password: status %d, body %s", wrongPwRec.Code, wrongPwRec.Body.String())
	}

	// Viewer token via invite key + correct password.
	viewerRec := doJSON(t, handler, http.MethodPost, "/api/rooms/"+created.ID+"/token", proto.TokenRequest{
		Name: "Alice", InviteKey: inviteKey, Password: "secret123",
	}, "")
	if viewerRec.Code != http.StatusOK {
		t.Fatalf("viewer token: status %d, body %s", viewerRec.Code, viewerRec.Body.String())
	}
	var viewerTok proto.TokenResponse
	decodeBody(t, viewerRec, &viewerTok)
	if viewerTok.Role != proto.RoleViewer {
		t.Errorf("role = %q, want viewer", viewerTok.Role)
	}
	if viewerTok.Token == "" || viewerTok.Session == "" {
		t.Error("expected non-empty token and session")
	}

	// Host token via host secret.
	hostRec := doJSON(t, handler, http.MethodPost, "/api/rooms/"+created.ID+"/token", proto.TokenRequest{
		Name: "Host Person", HostSecret: hostSecret,
	}, "")
	if hostRec.Code != http.StatusOK {
		t.Fatalf("host token: status %d, body %s", hostRec.Code, hostRec.Body.String())
	}
	var hostTok proto.TokenResponse
	decodeBody(t, hostRec, &hostTok)
	if hostTok.Role != proto.RoleHost {
		t.Errorf("role = %q, want host", hostTok.Role)
	}

	// Projector token: name ignored, fixed identity.
	projRec := doJSON(t, handler, http.MethodPost, "/api/rooms/"+created.ID+"/token", proto.TokenRequest{
		ProjectorKey: projectorKey,
	}, "")
	if projRec.Code != http.StatusOK {
		t.Fatalf("projector token: status %d, body %s", projRec.Code, projRec.Body.String())
	}
	var projTok proto.TokenResponse
	decodeBody(t, projRec, &projTok)
	if projTok.Role != proto.RoleProjector || projTok.Identity != "projector" {
		t.Errorf("projector token = %+v", projTok)
	}

	// Post a chat message as the viewer.
	postChatRec := doJSON(t, handler, http.MethodPost, "/api/rooms/"+created.ID+"/chat", map[string]string{
		"text": "hello world",
	}, viewerTok.Session)
	if postChatRec.Code != http.StatusCreated {
		t.Fatalf("post chat: status %d, body %s", postChatRec.Code, postChatRec.Body.String())
	}
	var posted proto.ChatMessage
	decodeBody(t, postChatRec, &posted)
	if posted.Text != "hello world" || posted.From.Identity != viewerTok.Identity {
		t.Errorf("posted message = %+v", posted)
	}

	// GET chat history.
	getChatRec := doJSON(t, handler, http.MethodGet, "/api/rooms/"+created.ID+"/chat", nil, viewerTok.Session)
	if getChatRec.Code != http.StatusOK {
		t.Fatalf("get chat: status %d, body %s", getChatRec.Code, getChatRec.Body.String())
	}
	var history proto.ChatHistoryResponse
	decodeBody(t, getChatRec, &history)
	if len(history.Messages) != 1 || history.Messages[0].Text != "hello world" {
		t.Errorf("history = %+v", history.Messages)
	}

	// Chat requires a session.
	if rec := doJSON(t, handler, http.MethodGet, "/api/rooms/"+created.ID+"/chat", nil, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("chat without session: status %d", rec.Code)
	}

	// Settings PATCH: viewer forbidden.
	viewerPatchRec := doJSON(t, handler, http.MethodPatch, "/api/rooms/"+created.ID+"/settings", map[string]any{
		"anyoneCanPause": true,
	}, viewerTok.Session)
	if viewerPatchRec.Code != http.StatusForbidden {
		t.Fatalf("viewer settings patch: status %d, body %s", viewerPatchRec.Code, viewerPatchRec.Body.String())
	}

	// Settings PATCH: host allowed. Patch to the opposite of whatever the
	// default is — only an actual change is broadcast, so patching to the
	// default value would assert nothing below.
	toggled := !hostTok.Settings.AnyoneCanPause
	hostPatchRec := doJSON(t, handler, http.MethodPatch, "/api/rooms/"+created.ID+"/settings", map[string]any{
		"anyoneCanPause": toggled,
	}, hostTok.Session)
	if hostPatchRec.Code != http.StatusOK {
		t.Fatalf("host settings patch: status %d, body %s", hostPatchRec.Code, hostPatchRec.Body.String())
	}
	var newSettings proto.RoomSettings
	decodeBody(t, hostPatchRec, &newSettings)
	if newSettings.AnyoneCanPause != toggled {
		t.Errorf("anyoneCanPause = %v after patch, want %v", newSettings.AnyoneCanPause, toggled)
	}

	// Broadcaster should have seen: chat message, settings, and a system message.
	broadcaster.mu.Lock()
	defer broadcaster.mu.Unlock()
	var sawChat, sawSettings, sawSystem bool
	for _, c := range broadcaster.calls {
		if c.RoomID != created.ID {
			t.Errorf("broadcast for wrong room: %q", c.RoomID)
		}
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
	if !sawChat {
		t.Error("expected a chat broadcast")
	}
	if !sawSettings {
		t.Error("expected a settings broadcast")
	}
	if !sawSystem {
		t.Error("expected a system chat broadcast for the settings change")
	}
}

func TestHealthz(t *testing.T) {
	handler, _, _ := newTestServer(t)
	rec := doJSON(t, handler, http.MethodGet, "/healthz", nil, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("healthz: status %d, body %q", rec.Code, rec.Body.String())
	}
}

func mustQueryParam(t *testing.T, link, key string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link %q: %v", link, err)
	}
	v := u.Query().Get(key)
	if v == "" {
		t.Fatalf("link %q missing query param %q", link, key)
	}
	return v
}
