// Package store implements SQLite-backed persistence for the room and its
// chat history using the pure-Go modernc.org/sqlite driver.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ShadowElf37/VideoStream/proto"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a lookup finds no matching row.
var ErrNotFound = errors.New("not found")

// Room is the one room's persisted state. There is exactly one row; it is
// created on first use and never deleted, only rotated.
type Room struct {
	// ViewerKey is the secret in the link friends get. Rotating it is how a
	// leaked link stops working.
	ViewerKey string
	Settings  proto.RoomSettings
	RotatedAt time.Time
	CreatedAt time.Time
}

// Store wraps a SQLite database handle.
type Store struct {
	db *sql.DB
}

// Open creates the database directory if needed, opens the SQLite database
// at path, and ensures the schema exists.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_foreign_keys=1")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// modernc.org/sqlite is not safe for unbounded concurrent writers.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		// The many-rooms layout that preceded the single room. It only ever
		// held test parties; the chat in it is not worth carrying across.
		`DROP TABLE IF EXISTS rooms`,
		`DROP TABLE IF EXISTS messages`,
		`CREATE TABLE IF NOT EXISTS room (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			viewer_key TEXT NOT NULL,
			settings_json TEXT NOT NULL,
			rotated_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS chat (
			id TEXT PRIMARY KEY,
			identity TEXT NOT NULL,
			name TEXT NOT NULL,
			color TEXT NOT NULL,
			text TEXT NOT NULL,
			ts INTEGER NOT NULL,
			kind TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_chat_ts ON chat(ts)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// DefaultSettings returns the settings the room starts with.
func DefaultSettings() proto.RoomSettings {
	return proto.RoomSettings{
		AnyoneCanPause:    true,
		DeafenImpliesMute: false,
		MaxPreset:         proto.Preset1080pHigh,
	}
}

// Room returns the room, creating it with a fresh key and default settings
// the first time it is asked for.
func (s *Store) Room(ctx context.Context) (*Room, error) {
	room, err := s.getRoom(ctx)
	if err == nil {
		return room, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	settingsJSON, err := json.Marshal(DefaultSettings())
	if err != nil {
		return nil, fmt.Errorf("marshal settings: %w", err)
	}
	now := time.Now().UTC().Unix()
	// INSERT OR IGNORE: with a single connection there is no race, but it
	// keeps a second Open on the same file harmless.
	_, err = s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO room (id, viewer_key, settings_json, rotated_at, created_at) VALUES (1, ?, ?, ?, ?)`,
		NewID(24), string(settingsJSON), now, now)
	if err != nil {
		return nil, fmt.Errorf("create room: %w", err)
	}
	return s.getRoom(ctx)
}

func (s *Store) getRoom(ctx context.Context) (*Room, error) {
	row := s.db.QueryRowContext(ctx, `SELECT viewer_key, settings_json, rotated_at, created_at FROM room WHERE id = 1`)
	var (
		room         Room
		settingsJSON string
		rotatedAt    int64
		createdAt    int64
	)
	err := row.Scan(&room.ViewerKey, &settingsJSON, &rotatedAt, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get room: %w", err)
	}
	if err := json.Unmarshal([]byte(settingsJSON), &room.Settings); err != nil {
		return nil, fmt.Errorf("unmarshal settings: %w", err)
	}
	room.RotatedAt = time.Unix(rotatedAt, 0).UTC()
	room.CreatedAt = time.Unix(createdAt, 0).UTC()
	return &room, nil
}

// RotateViewerKey replaces the viewer key and returns the room as it now is.
func (s *Store) RotateViewerKey(ctx context.Context) (*Room, error) {
	if _, err := s.Room(ctx); err != nil {
		return nil, err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE room SET viewer_key = ?, rotated_at = ? WHERE id = 1`,
		NewID(24), time.Now().UTC().Unix())
	if err != nil {
		return nil, fmt.Errorf("rotate key: %w", err)
	}
	return s.getRoom(ctx)
}

// UpdateSettings persists new settings.
func (s *Store) UpdateSettings(ctx context.Context, settings proto.RoomSettings) error {
	if _, err := s.Room(ctx); err != nil {
		return err
	}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE room SET settings_json = ? WHERE id = 1`, string(settingsJSON)); err != nil {
		return fmt.Errorf("update settings: %w", err)
	}
	return nil
}

// InsertMessage stores a chat message.
func (s *Store) InsertMessage(ctx context.Context, msg proto.ChatMessage) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chat (id, identity, name, color, text, ts, kind) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.From.Identity, msg.From.Name, msg.From.Color, msg.Text, msg.TS, msg.Kind,
	)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	return nil
}

// ListMessages returns up to limit messages older than before (unix
// milliseconds; 0 means no bound), oldest-first.
func (s *Store) ListMessages(ctx context.Context, before int64, limit int) ([]proto.ChatMessage, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if before > 0 {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, identity, name, color, text, ts, kind FROM chat WHERE ts < ? ORDER BY ts DESC LIMIT ?`,
			before, limit)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, identity, name, color, text, ts, kind FROM chat ORDER BY ts DESC LIMIT ?`,
			limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	msgs := []proto.ChatMessage{}
	for rows.Next() {
		var m proto.ChatMessage
		if err := rows.Scan(&m.ID, &m.From.Identity, &m.From.Name, &m.From.Color, &m.Text, &m.TS, &m.Kind); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}

	// Reverse to oldest-first.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}
