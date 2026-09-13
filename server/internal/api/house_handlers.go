package api

// The house projector: a projector process running on this server, streaming
// files pushed with vspush.
//
// It plays no favourites between rooms and holds no state of its own — it
// asks this server which room it should be serving, and joins or leaves as
// the answer changes. That indirection is what lets a host turn it on from
// the web UI instead of SSHing somewhere to start a binary, and it means
// exactly one room can have it at a time, which is the honest model: there is
// one machine and it has one uplink.

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"

	"github.com/ShadowElf37/VideoStream/proto"
)

// houseAssignment is the room the house projector should serve, or empty.
type houseAssignment struct {
	RoomID       string `json:"roomId"`
	ProjectorKey string `json:"projectorKey"`
}

// houseState is the single in-memory assignment. It is deliberately not
// persisted: on a restart nothing is playing anyway, and a stale assignment
// would have the projector rejoin a room whose party ended days ago.
type houseState struct {
	mu sync.RWMutex
	at houseAssignment
}

func (h *houseState) get() houseAssignment {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.at
}

func (h *houseState) set(a houseAssignment) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.at = a
}

// handleHouseAssignment is polled by the house projector itself. It is
// authenticated with a shared secret rather than a session, because the
// caller is a service, and it returns the projector key — so the secret must
// be treated as equivalent to every room's projector key.
func (s *Server) handleHouseAssignment(w http.ResponseWriter, r *http.Request) {
	if s.cfg.HouseSecret == "" {
		writeError(w, http.StatusNotFound, "no house projector is configured")
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.HouseSecret)) != 1 {
		writeError(w, http.StatusUnauthorized, "bad house secret")
		return
	}
	writeJSON(w, http.StatusOK, s.house.get())
}

// handleSetHouseProjector hands the house projector to this room, or takes it
// away. Host-only: it decides what everyone in the room watches.
func (s *Server) handleSetHouseProjector(w http.ResponseWriter, r *http.Request) {
	if s.cfg.HouseSecret == "" {
		writeError(w, http.StatusNotFound, "no house projector is configured on this server")
		return
	}
	id := r.PathValue("id")
	sess := sessionFromContext(r.Context())
	if sess.Role != proto.RoleHost {
		writeError(w, http.StatusForbidden, "host role required")
		return
	}
	room, err := s.getRoomOr404(w, r.Context(), id)
	if err != nil {
		return
	}

	if r.Method == http.MethodDelete {
		if s.house.get().RoomID == room.ID {
			s.house.set(houseAssignment{})
			if err := s.chat.System(r.Context(), room.ID, "The server projector left the room."); err != nil {
				s.logger.Warn("house: system message failed", "err", err)
			}
		}
		writeJSON(w, http.StatusOK, houseStatus{Active: false})
		return
	}

	prev := s.house.get()
	if prev.RoomID != "" && prev.RoomID != room.ID {
		// One projector, one room. Saying so beats silently stealing it from
		// whoever is mid-film.
		writeError(w, http.StatusConflict, "the server projector is already in another room")
		return
	}
	s.house.set(houseAssignment{RoomID: room.ID, ProjectorKey: room.ProjectorKey})
	if prev.RoomID != room.ID {
		if err := s.chat.System(r.Context(), room.ID, "The server projector is joining; pick something from the Queue tab."); err != nil {
			s.logger.Warn("house: system message failed", "err", err)
		}
	}
	writeJSON(w, http.StatusOK, houseStatus{Active: true})
}

type houseStatus struct {
	Active bool `json:"active"`
}

// handleGetHouseProjector reports whether this room currently has it, so the
// UI can show the right button without guessing.
func (s *Server) handleGetHouseProjector(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	at := s.house.get()
	writeJSON(w, http.StatusOK, struct {
		houseStatus
		Available bool `json:"available"`
		Elsewhere bool `json:"elsewhere"`
	}{
		houseStatus: houseStatus{Active: at.RoomID == id},
		Available:   s.cfg.HouseSecret != "",
		Elsewhere:   at.RoomID != "" && at.RoomID != id,
	})
}
