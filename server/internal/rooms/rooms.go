// Package rooms is the room: its viewer key, its settings, and what a
// visitor's credentials are worth. The name is plural for history; there is
// exactly one.
package rooms

import (
	"context"
	"crypto/subtle"
	"fmt"
	"strings"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/ShadowElf37/VideoStream/server/internal/config"
	"github.com/ShadowElf37/VideoStream/server/internal/store"
)

// Service wraps the store with room-level logic.
type Service struct {
	store *store.Store
	cfg   *config.Config
}

// NewService creates a rooms Service.
func NewService(st *store.Store, cfg *config.Config) *Service {
	return &Service{store: st, cfg: cfg}
}

// Get returns the room, creating it on first use.
func (s *Service) Get(ctx context.Context) (*store.Room, error) {
	return s.store.Room(ctx)
}

// Rotate replaces the viewer key. Every link handed out so far stops
// working; devices that have been in before are remembered by their cookie
// and unaffected.
func (s *Service) Rotate(ctx context.Context) (*store.Room, error) {
	room, err := s.store.RotateViewerKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("rotate viewer key: %w", err)
	}
	return room, nil
}

// UpdateSettings persists new settings.
func (s *Service) UpdateSettings(ctx context.Context, settings proto.RoomSettings) error {
	return s.store.UpdateSettings(ctx, settings)
}

// Links builds the viewer link from the configured public URL: the site
// root with the key in the query, because the site root is the door.
func (s *Service) Links(room *store.Room) proto.Links {
	base := strings.TrimSuffix(s.cfg.PublicURL, "/")
	return proto.Links{Viewer: fmt.Sprintf("%s/?k=%s", base, room.ViewerKey)}
}

// KeyAccess is what a key from a link is worth: viewer if it is the current
// viewer key, none otherwise. Compared in constant time.
func (s *Service) KeyAccess(room *store.Room, key string) string {
	if key != "" && subtle.ConstantTimeCompare([]byte(key), []byte(room.ViewerKey)) == 1 {
		return proto.AccessViewer
	}
	return proto.AccessNone
}

// PasswordAccess is what the room password is worth: host if it matches,
// none otherwise. Compared in constant time.
func (s *Service) PasswordAccess(password string) string {
	if password != "" && subtle.ConstantTimeCompare([]byte(password), []byte(s.cfg.RoomPassword)) == 1 {
		return proto.AccessHost
	}
	return proto.AccessNone
}

// rank orders access levels so the best of several can be picked.
func rank(access string) int {
	switch access {
	case proto.AccessHost:
		return 2
	case proto.AccessViewer:
		return 1
	default:
		return 0
	}
}

// Best returns the highest access level among those given.
func Best(levels ...string) string {
	best := proto.AccessNone
	for _, l := range levels {
		if rank(l) > rank(best) {
			best = l
		}
	}
	return best
}

// RoleFor maps an access level to the role it grants; none maps to "".
func RoleFor(access string) string {
	switch access {
	case proto.AccessHost:
		return proto.RoleHost
	case proto.AccessViewer:
		return proto.RoleViewer
	default:
		return ""
	}
}
