package chat

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/ShadowElf37/VideoStream/server/internal/store"
)

type recordingBroadcaster struct {
	mu    sync.Mutex
	calls []string // topics
}

func (b *recordingBroadcaster) Broadcast(_ context.Context, _, topic string, _ []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, topic)
	return nil
}

func (b *recordingBroadcaster) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.calls)
}

func newTestService(t *testing.T) (*Service, *recordingBroadcaster, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	b := &recordingBroadcaster{}
	return NewService(st, b), b, st
}

func TestPostMessageValidation(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if _, err := svc.PostMessage(ctx, "room1", "id", "Name", "#fff", "   "); err != ErrEmptyText {
		t.Errorf("empty text: err = %v", err)
	}
	long := make([]byte, 2001)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := svc.PostMessage(ctx, "room1", "id", "Name", "#fff", string(long)); err != ErrTextTooLong {
		t.Errorf("too long: err = %v", err)
	}
}

func TestPostMessageStoresAndBroadcasts(t *testing.T) {
	svc, b, _ := newTestService(t)
	ctx := context.Background()

	msg, err := svc.PostMessage(ctx, "room1", "alice-ab12", "Alice", "#e57373", "hi there")
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if msg.Kind != "user" || msg.From.Identity != "alice-ab12" {
		t.Errorf("msg = %+v", msg)
	}
	if b.count() != 1 {
		t.Errorf("expected 1 broadcast, got %d", b.count())
	}

	history, err := svc.History(ctx, "room1", 0, 100)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 || history[0].ID != msg.ID {
		t.Errorf("history = %+v", history)
	}
}

func TestSystemMessage(t *testing.T) {
	svc, b, _ := newTestService(t)
	ctx := context.Background()

	if err := svc.System(ctx, "room1", "Host changed room settings: anyone can pause = on"); err != nil {
		t.Fatalf("System: %v", err)
	}
	history, err := svc.History(ctx, "room1", 0, 100)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 || history[0].Kind != "system" || history[0].From.Identity != "system" {
		t.Errorf("history = %+v", history)
	}
	if b.count() != 1 {
		t.Errorf("expected 1 broadcast, got %d", b.count())
	}
}

func TestRateLimiting(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := svc.PostMessage(ctx, "room1", "id", "Name", "#fff", "msg"); err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
	}
	if _, err := svc.PostMessage(ctx, "room1", "id", "Name", "#fff", "one too many"); err != ErrRateLimited {
		t.Errorf("6th message: err = %v, want ErrRateLimited", err)
	}
}

func TestBroadcastSettings(t *testing.T) {
	svc, b, _ := newTestService(t)
	settings := proto.RoomSettings{AnyoneCanPause: true, MaxPreset: proto.Preset720p}
	if err := svc.BroadcastSettings(context.Background(), "room1", settings); err != nil {
		t.Fatalf("BroadcastSettings: %v", err)
	}
	if b.count() != 1 || b.calls[0] != proto.TopicSettings {
		t.Errorf("calls = %v", b.calls)
	}
}

func TestLimiterAllow(t *testing.T) {
	l := NewLimiter(2, 100*time.Millisecond)
	if !l.Allow("k") || !l.Allow("k") {
		t.Fatal("expected first two calls to be allowed")
	}
	if l.Allow("k") {
		t.Fatal("expected third call to be denied")
	}
	time.Sleep(120 * time.Millisecond)
	if !l.Allow("k") {
		t.Fatal("expected call to be allowed after refill")
	}
}
