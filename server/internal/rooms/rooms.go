// Package rooms implements room creation and lookup on top of the store,
// including building the public links returned to clients.
package rooms

import (
	"context"
	"fmt"
	"strings"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/ShadowElf37/VideoStream/server/internal/config"
	"github.com/ShadowElf37/VideoStream/server/internal/store"
)

// defaultRoomName is used when a room is created without a name.
const defaultRoomName = "Untitled Room"

// Service wraps the store with room-level business logic.
type Service struct {
	store *store.Store
	cfg   *config.Config
}

// NewService creates a rooms Service.
func NewService(st *store.Store, cfg *config.Config) *Service {
	return &Service{store: st, cfg: cfg}
}

// Create makes a new room with default settings.
func (s *Service) Create(ctx context.Context, name string, passwordHash *string) (*store.Room, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultRoomName
	}
	room, err := s.store.CreateRoom(ctx, name, passwordHash, store.DefaultSettings())
	if err != nil {
		return nil, fmt.Errorf("create room: %w", err)
	}
	return room, nil
}

// Get fetches a room by ID. Returns store.ErrNotFound if missing.
func (s *Service) Get(ctx context.Context, id string) (*store.Room, error) {
	return s.store.GetRoom(ctx, id)
}

// UpdateSettings persists new settings for a room.
func (s *Service) UpdateSettings(ctx context.Context, id string, settings proto.RoomSettings) error {
	return s.store.UpdateSettings(ctx, id, settings)
}

// CreateResponse builds the CreateRoomResponse with links pointing at the
// configured public URL.
func (s *Service) CreateResponse(room *store.Room) proto.CreateRoomResponse {
	base := strings.TrimSuffix(s.cfg.PublicURL, "/")
	return proto.CreateRoomResponse{
		ID:            room.ID,
		Name:          room.Name,
		InviteLink:    fmt.Sprintf("%s/r/%s?k=%s", base, room.ID, room.InviteKey),
		HostLink:      fmt.Sprintf("%s/r/%s?h=%s", base, room.ID, room.HostSecret),
		ProjectorLink: fmt.Sprintf("%s/r/%s?p=%s", base, room.ID, room.ProjectorKey),
	}
}

// Info builds the public RoomInfo view of a room.
func Info(room *store.Room) proto.RoomInfo {
	return proto.RoomInfo{
		ID:          room.ID,
		Name:        room.Name,
		HasPassword: room.PasswordHash != nil,
		Settings:    room.Settings,
	}
}
