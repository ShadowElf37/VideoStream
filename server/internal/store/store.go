// Package store implements SQLite-backed persistence for rooms and chat
// messages using the pure-Go modernc.org/sqlite driver.
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

// Room is a persisted room row.
type Room struct {
	ID           string
	Name         string
	PasswordHash *string // nil means no password
	InviteKey    string
	HostSecret   string
	ProjectorKey string
	Settings     proto.RoomSettings
	CreatedAt    time.Time
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
		`CREATE TABLE IF NOT EXISTS rooms (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			password_hash TEXT,
			invite_key TEXT NOT NULL,
			host_secret TEXT NOT NULL,
			projector_key TEXT NOT NULL,
			settings_json TEXT NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			room_id TEXT NOT NULL,
			identity TEXT NOT NULL,
			name TEXT NOT NULL,
			color TEXT NOT NULL,
			text TEXT NOT NULL,
			ts INTEGER NOT NULL,
			kind TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_room_ts ON messages(room_id, ts)`,
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

// DefaultSettings returns the settings applied to newly created rooms.
func DefaultSettings() proto.RoomSettings {
	return proto.RoomSettings{
		AnyoneCanPause:    false,
		DeafenImpliesMute: false,
		MaxPreset:         proto.Preset1080pHigh,
	}
}

// CreateRoom generates a new room ID and secrets, persists the row, and
// returns the created Room.
func (s *Store) CreateRoom(ctx context.Context, name string, passwordHash *string, settings proto.RoomSettings) (*Room, error) {
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("marshal settings: %w", err)
	}

	room := &Room{
		ID:           NewID(8),
		Name:         name,
		PasswordHash: passwordHash,
		InviteKey:    NewID(24),
		HostSecret:   NewID(24),
		ProjectorKey: NewID(24),
		Settings:     settings,
		CreatedAt:    time.Now().UTC(),
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO rooms (id, name, password_hash, invite_key, host_secret, projector_key, settings_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		room.ID, room.Name, room.PasswordHash, room.InviteKey, room.HostSecret, room.ProjectorKey,
		string(settingsJSON), room.CreatedAt.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("insert room: %w", err)
	}
	return room, nil
}

// GetRoom fetches a room by ID.
func (s *Store) GetRoom(ctx context.Context, id string) (*Room, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, password_hash, invite_key, host_secret, projector_key, settings_json, created_at
		 FROM rooms WHERE id = ?`, id)

	var (
		room         Room
		passwordHash sql.NullString
		settingsJSON string
		createdAt    int64
	)
	err := row.Scan(&room.ID, &room.Name, &passwordHash, &room.InviteKey, &room.HostSecret,
		&room.ProjectorKey, &settingsJSON, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get room: %w", err)
	}
	if passwordHash.Valid {
		v := passwordHash.String
		room.PasswordHash = &v
	}
	if err := json.Unmarshal([]byte(settingsJSON), &room.Settings); err != nil {
		return nil, fmt.Errorf("unmarshal settings: %w", err)
	}
	room.CreatedAt = time.Unix(createdAt, 0).UTC()
	return &room, nil
}

// UpdateSettings persists new settings for a room.
func (s *Store) UpdateSettings(ctx context.Context, id string, settings proto.RoomSettings) error {
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE rooms SET settings_json = ? WHERE id = ?`, string(settingsJSON), id)
	if err != nil {
		return fmt.Errorf("update settings: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// InsertMessage stores a chat message.
func (s *Store) InsertMessage(ctx context.Context, msg proto.ChatMessage) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (id, room_id, identity, name, color, text, ts, kind)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.RoomID, msg.From.Identity, msg.From.Name, msg.From.Color, msg.Text, msg.TS, msg.Kind,
	)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	return nil
}

// ListMessages returns up to limit messages for a room older than before
// (unix milliseconds; 0 means no lower bound on recency), newest-first
// internally but returned oldest-first for display.
func (s *Store) ListMessages(ctx context.Context, roomID string, before int64, limit int) ([]proto.ChatMessage, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if before > 0 {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, room_id, identity, name, color, text, ts, kind
			 FROM messages WHERE room_id = ? AND ts < ? ORDER BY ts DESC LIMIT ?`,
			roomID, before, limit)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, room_id, identity, name, color, text, ts, kind
			 FROM messages WHERE room_id = ? ORDER BY ts DESC LIMIT ?`,
			roomID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	msgs := []proto.ChatMessage{}
	for rows.Next() {
		var m proto.ChatMessage
		if err := rows.Scan(&m.ID, &m.RoomID, &m.From.Identity, &m.From.Name, &m.From.Color, &m.Text, &m.TS, &m.Kind); err != nil {
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
