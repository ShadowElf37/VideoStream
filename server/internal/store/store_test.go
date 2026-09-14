package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ShadowElf37/VideoStream/proto"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRoomIsCreatedOnceAndStable(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	room, err := st.Room(ctx)
	if err != nil {
		t.Fatalf("Room: %v", err)
	}
	if len(room.ViewerKey) != 24 {
		t.Errorf("viewer key length = %d, want 24", len(room.ViewerKey))
	}
	if room.Settings != DefaultSettings() {
		t.Errorf("settings = %+v, want defaults", room.Settings)
	}

	again, err := st.Room(ctx)
	if err != nil {
		t.Fatalf("Room again: %v", err)
	}
	if again.ViewerKey != room.ViewerKey {
		t.Error("asking for the room twice produced two different keys")
	}
}

func TestRotateViewerKey(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	before, err := st.Room(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, err := st.RotateViewerKey(ctx)
	if err != nil {
		t.Fatalf("RotateViewerKey: %v", err)
	}
	if after.ViewerKey == before.ViewerKey {
		t.Error("rotation kept the same key")
	}
	if len(after.ViewerKey) != 24 {
		t.Errorf("rotated key length = %d", len(after.ViewerKey))
	}
	if after.RotatedAt.Before(before.RotatedAt) {
		t.Error("rotated_at went backwards")
	}
	if after.Settings != before.Settings {
		t.Error("rotation changed the settings")
	}
}

func TestUpdateSettings(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	newSettings := proto.RoomSettings{AnyoneCanPause: false, DeafenImpliesMute: true, MaxPreset: proto.Preset720p}
	if err := st.UpdateSettings(ctx, newSettings); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	got, err := st.Room(ctx)
	if err != nil {
		t.Fatalf("Room: %v", err)
	}
	if got.Settings != newSettings {
		t.Errorf("settings = %+v, want %+v", got.Settings, newSettings)
	}
}

func TestMessagesInsertAndList(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	base := int64(1_700_000_000_000)
	for i := int64(0); i < 5; i++ {
		msg := proto.ChatMessage{
			ID:   NewID(12),
			From: proto.ChatAuthor{Identity: "alice-ab12", Name: "Alice", Color: "#fff"},
			Text: "hello",
			TS:   base + i,
			Kind: "user",
		}
		if err := st.InsertMessage(ctx, msg); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	all, err := st.ListMessages(ctx, 0, 100)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("len(all) = %d, want 5", len(all))
	}
	for i := 0; i < len(all)-1; i++ {
		if all[i].TS > all[i+1].TS {
			t.Fatalf("expected oldest-first order, got %v", all)
		}
	}

	limited, err := st.ListMessages(ctx, 0, 2)
	if err != nil {
		t.Fatalf("ListMessages limited: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("len(limited) = %d, want 2", len(limited))
	}
	// The newest 2 of 5, oldest-first: indices 3, 4.
	if limited[0].TS != base+3 || limited[1].TS != base+4 {
		t.Errorf("limited = %+v", limited)
	}

	before, err := st.ListMessages(ctx, base+3, 100)
	if err != nil {
		t.Fatalf("ListMessages before: %v", err)
	}
	if len(before) != 3 {
		t.Fatalf("len(before) = %d, want 3", len(before))
	}
}

func TestListMessagesEmpty(t *testing.T) {
	st := newTestStore(t)
	msgs, err := st.ListMessages(context.Background(), 0, 100)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if msgs == nil || len(msgs) != 0 {
		t.Errorf("expected empty non-nil slice, got %v", msgs)
	}
}

// The legacy many-rooms tables must not survive an upgrade: they had a
// different messages shape and nothing reads them any more.
func TestLegacyTablesAreDropped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`CREATE TABLE rooms (id TEXT PRIMARY KEY); CREATE TABLE messages (id TEXT PRIMARY KEY, room_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	st.Close()

	st, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st.Close()
	var n int
	if err := st.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('rooms','messages')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d legacy tables still present", n)
	}
}
