package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/ShadowElf37/VideoStream/server/internal/rooms"
	"github.com/ShadowElf37/VideoStream/server/internal/store"
	"github.com/ShadowElf37/VideoStream/server/internal/tokens"
)

func (s *Server) handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	var req proto.CreateRoomRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	var passwordHash *string
	if req.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			s.logger.Error("hash room password failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		h := string(hash)
		passwordHash = &h
	}

	room, err := s.rooms.Create(r.Context(), req.Name, passwordHash)
	if err != nil {
		s.logger.Error("create room failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusCreated, s.rooms.CreateResponse(room))
}

func (s *Server) handleGetRoom(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	room, err := s.getRoomOr404(w, r.Context(), id)
	if err != nil {
		return
	}
	writeJSON(w, http.StatusOK, rooms.Info(room))
}

// getRoomOr404 fetches a room, writing a 404 or 500 response and returning
// a non-nil error if it can't be found.
func (s *Server) getRoomOr404(w http.ResponseWriter, ctx context.Context, id string) (*store.Room, error) {
	room, err := s.rooms.Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "room not found")
		return nil, err
	}
	if err != nil {
		s.logger.Error("get room failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return nil, err
	}
	return room, nil
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req proto.TokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	room, err := s.getRoomOr404(w, r.Context(), id)
	if err != nil {
		return
	}

	role, providedKey, roomKey, err := resolveRole(req, room)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if subtle.ConstantTimeCompare([]byte(providedKey), []byte(roomKey)) != 1 {
		writeError(w, http.StatusForbidden, "invalid key")
		return
	}

	if role == proto.RoleViewer && room.PasswordHash != nil {
		if err := bcrypt.CompareHashAndPassword([]byte(*room.PasswordHash), []byte(req.Password)); err != nil {
			writeError(w, http.StatusForbidden, "invalid password")
			return
		}
	}

	var identity, name string
	if role == proto.RoleProjector {
		identity = "projector"
		name = "Projector"
	} else {
		name = strings.TrimSpace(req.Name)
		if name == "" || len(name) > 32 {
			writeError(w, http.StatusBadRequest, "name must be 1-32 characters")
			return
		}
		identity = tokens.NewIdentity(name)
	}
	color := tokens.ColorFor(identity)

	jwt, err := tokens.MintLiveKitToken(s.cfg.LiveKitAPIKey, s.cfg.LiveKitAPISecret, id, identity, name, role, color)
	if err != nil {
		s.logger.Error("mint livekit token failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := tokens.EnsureRoom(r.Context(), s.lkClient, id); err != nil {
		s.logger.Warn("ensure livekit room failed", "room", id, "err", err)
	}

	sess := tokens.Session{
		RoomID:   id,
		Identity: identity,
		Name:     name,
		Color:    color,
		Role:     role,
		Exp:      time.Now().Add(tokens.TokenValidity()).Unix(),
	}
	signed, err := tokens.Sign(s.cfg.SessionSecret, sess)
	if err != nil {
		s.logger.Error("sign session failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, proto.TokenResponse{
		Token:    jwt,
		URL:      s.cfg.LiveKitURL,
		Identity: identity,
		Role:     role,
		Color:    color,
		Session:  signed,
		Settings: room.Settings,
	})
}

// resolveRole determines which single key was supplied, the role it grants,
// and the room's matching secret to compare it against.
func resolveRole(req proto.TokenRequest, room *store.Room) (role, providedKey, roomKey string, err error) {
	n := 0
	if req.InviteKey != "" {
		n++
	}
	if req.HostSecret != "" {
		n++
	}
	if req.ProjectorKey != "" {
		n++
	}
	if n != 1 {
		return "", "", "", errExactlyOneKey
	}
	switch {
	case req.InviteKey != "":
		return proto.RoleViewer, req.InviteKey, room.InviteKey, nil
	case req.HostSecret != "":
		return proto.RoleHost, req.HostSecret, room.HostSecret, nil
	default:
		return proto.RoleProjector, req.ProjectorKey, room.ProjectorKey, nil
	}
}

var errExactlyOneKey = errors.New("exactly one of inviteKey, hostSecret, or projectorKey is required")
