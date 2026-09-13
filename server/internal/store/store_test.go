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

func TestCreateAndGetRoom(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	settings := DefaultSettings()
	room, err := st.CreateRoom(ctx, "Movie Night", nil, settings)
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	if len(room.ID) != 8 {
		t.Errorf("room ID length = %d, want 8", len(room.ID))
	}
	for _, key := range []string{room.InviteKey, room.HostSecret, room.ProjectorKey} {
		if len(key) != 24 {
			t.Errorf("key length = %d, want 24", len(key))
		}
	}
	if room.InviteKey == room.HostSecret || room.HostSecret == room.ProjectorKey {
		t.Error("keys should be distinct")
	}

	got, err := st.GetRoom(ctx, room.ID)
	if err != nil {
		t.Fatalf("GetRoom: %v", err)
	}
	if got.Name != "Movie Night" {
		t.Errorf("name = %q", got.Name)
	}
	if got.PasswordHash != nil {
		t.Error("expected no password hash")
	}
	if got.Settings != settings {
		t.Errorf("settings = %+v, want %+v", got.Settings, settings)
	}
}

func TestGetRoomNotFound(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.GetRoom(context.Background(), "nope"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRoomWithPassword(t *testing.T) {
	st := newTestStore(t)
	hash := "bcrypt-hash-stand-in"
	room, err := st.CreateRoom(context.Background(), "", &hash, DefaultSettings())
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	got, err := st.GetRoom(context.Background(), room.ID)
	if err != nil {
		t.Fatalf("GetRoom: %v", err)
	}
	if got.PasswordHash == nil || *got.PasswordHash != hash {
		t.Errorf("password hash = %v, want %q", got.PasswordHash, hash)
	}
}

func TestUpdateSettings(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	room, err := st.CreateRoom(ctx, "Room", nil, DefaultSettings())
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}

	newSettings := proto.RoomSettings{AnyoneCanPause: true, DeafenImpliesMute: true, MaxPreset: proto.Preset720p}
	if err := st.UpdateSettings(ctx, room.ID, newSettings); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	got, err := st.GetRoom(ctx, room.ID)
	if err != nil {
		t.Fatalf("GetRoom: %v", err)
	}
	if got.Settings != newSettings {
		t.Errorf("settings = %+v, want %+v", got.Settings, newSettings)
	}
}

func TestUpdateSettingsNotFound(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpdateSettings(context.Background(), "nope", DefaultSettings()); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMessagesInsertAndList(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	room, err := st.CreateRoom(ctx, "Room", nil, DefaultSettings())
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}

	base := int64(1_700_000_000_000)
	for i := int64(0); i < 5; i++ {
		msg := proto.ChatMessage{
			ID:     NewID(12),
			RoomID: room.ID,
			From:   proto.ChatAuthor{Identity: "alice-ab12", Name: "Alice", Color: "#fff"},
			Text:   "hello",
			TS:     base + i,
			Kind:   "user",
		}
		if err := st.InsertMessage(ctx, msg); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	all, err := st.ListMessages(ctx, room.ID, 0, 100)
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

	limited, err := st.ListMessages(ctx, room.ID, 0, 2)
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

	before, err := st.ListMessages(ctx, room.ID, base+3, 100)
	if err != nil {
		t.Fatalf("ListMessages before: %v", err)
	}
	if len(before) != 3 {
		t.Fatalf("len(before) = %d, want 3", len(before))
	}
}

func TestListMessagesEmpty(t *testing.T) {
	st := newTestStore(t)
	msgs, err := st.ListMessages(context.Background(), "no-such-room", 0, 100)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if msgs == nil || len(msgs) != 0 {
		t.Errorf("expected empty non-nil slice, got %v", msgs)
	}
}
