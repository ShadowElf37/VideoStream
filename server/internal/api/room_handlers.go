package api

// The door. Three things get a visitor through it: the viewer key in a
// link, the room password, and the cookie this file sets on the way in.

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/ShadowElf37/VideoStream/server/internal/rooms"
	"github.com/ShadowElf37/VideoStream/server/internal/store"
	"github.com/ShadowElf37/VideoStream/server/internal/tokens"
)

// authCookie remembers the role a device earned. Long-lived on purpose; see
// tokens.Remember.
const authCookie = "vs_auth"

// rememberFor is as long as browsers will keep a cookie at all.
const rememberFor = 400 * 24 * time.Hour

var errNoCredentials = errors.New("this room needs a link, the password, or a device that has been here before")

// cookieAccess is what the visitor's cookie is worth, or none.
func (s *Server) cookieAccess(r *http.Request) string {
	c, err := r.Cookie(authCookie)
	if err != nil {
		return proto.AccessNone
	}
	rem, err := tokens.VerifyRemember(s.cfg.SessionSecret, c.Value)
	if err != nil {
		return proto.AccessNone
	}
	switch rem.Role {
	case proto.RoleHost:
		return proto.AccessHost
	case proto.RoleViewer:
		return proto.AccessViewer
	}
	return proto.AccessNone
}

// setCookie remembers access on the device. It only ever upgrades: a host
// who opens a friend's viewer link does not lose the host cookie.
func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, access string) {
	if access == proto.AccessNone {
		return
	}
	if current := s.cookieAccess(r); rooms.Best(current, access) == current && current == access {
		return // already remembered at this level
	}
	if rooms.Best(s.cookieAccess(r), access) != access {
		return // would be a downgrade
	}
	value, err := tokens.SignRemember(s.cfg.SessionSecret, tokens.Remember{Role: rooms.RoleFor(access)})
	if err != nil {
		s.logger.Error("sign remember cookie", "err", err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   int(rememberFor.Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// clientIP is the address password attempts are throttled by. Caddy sets
// X-Forwarded-For; without a proxy the peer address is the truth.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// handleGetRoom tells a visitor what they already hold, so the door can ask
// for exactly what is missing.
func (s *Server) handleGetRoom(w http.ResponseWriter, r *http.Request) {
	room, err := s.rooms.Get(r.Context())
	if err != nil {
		s.logger.Error("get room failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	access := rooms.Best(s.cookieAccess(r), s.rooms.KeyAccess(room, r.URL.Query().Get("key")))
	info := proto.RoomInfo{Access: access}
	if access != proto.AccessNone {
		info.Occupants = s.watcher.Count()
	}
	writeJSON(w, http.StatusOK, info)
}

// resolveAccess works out the best access the request carries, checking the
// password last so a throttled attempt never even reaches the comparison.
func (s *Server) resolveAccess(w http.ResponseWriter, r *http.Request, room *store.Room, req proto.TokenRequest) (string, bool) {
	access := rooms.Best(s.cookieAccess(r), s.rooms.KeyAccess(room, req.Key))
	if req.Password != "" && access != proto.AccessHost {
		if !s.pwLimiter.Allow(clientIP(r)) {
			writeError(w, http.StatusTooManyRequests, "too many password attempts; wait half a minute")
			return "", false
		}
		if s.rooms.PasswordAccess(req.Password) != proto.AccessHost {
			writeError(w, http.StatusForbidden, "wrong password")
			return "", false
		}
		access = proto.AccessHost
	}
	if access == proto.AccessNone {
		if req.Key != "" {
			writeError(w, http.StatusForbidden, "this link is no longer valid; ask for a new one, or enter the password")
		} else {
			writeError(w, http.StatusForbidden, errNoCredentials.Error())
		}
		return "", false
	}
	return access, true
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	var req proto.TokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	room, err := s.rooms.Get(r.Context())
	if err != nil {
		s.logger.Error("get room failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	access, ok := s.resolveAccess(w, r, room, req)
	if !ok {
		return
	}

	var identity, name, role string
	if req.Projector {
		if access != proto.AccessHost {
			writeError(w, http.StatusForbidden, "the projector needs the room password")
			return
		}
		identity, name, role = "projector", "Projector", proto.RoleProjector
	} else {
		name = strings.TrimSpace(req.Name)
		if name == "" || len(name) > 32 {
			writeError(w, http.StatusBadRequest, "name must be 1-32 characters")
			return
		}
		identity = tokens.NewIdentity(name)
		role = rooms.RoleFor(access)
	}
	color := tokens.ColorFor(identity)

	jwt, err := tokens.MintLiveKitToken(s.cfg.LiveKitAPIKey, s.cfg.LiveKitAPISecret, identity, name, role, color)
	if err != nil {
		s.logger.Error("mint livekit token failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tokens.EnsureRoom(r.Context(), s.lkClient); err != nil {
		s.logger.Warn("ensure livekit room failed", "err", err)
	}

	sess := tokens.Session{
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

	// The projector is a process, not a device; remembering it would be odd
	// and its cookie jar is thrown away anyway.
	if !req.Projector {
		s.setCookie(w, r, access)
	}

	writeJSON(w, http.StatusOK, proto.TokenResponse{
		Token:    jwt,
		URL:      s.cfg.LiveKitURL,
		Identity: identity,
		Role:     role,
		Color:    color,
		Session:  signed,
		Settings: room.Settings,
		Links:    s.rooms.Links(room),
	})
}

// handleLogout forgets this device.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetLinks(w http.ResponseWriter, r *http.Request) {
	room, err := s.rooms.Get(r.Context())
	if err != nil {
		s.logger.Error("get room failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, s.rooms.Links(room))
}

// handleRotateLinks is the host's "that link got out" button.
func (s *Server) handleRotateLinks(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r.Context())
	if sess.Role != proto.RoleHost {
		writeError(w, http.StatusForbidden, "host role required")
		return
	}
	room, err := s.rooms.Rotate(r.Context())
	if err != nil {
		s.logger.Error("rotate links failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.logger.Info("invite link rotated", "by", sess.Identity)
	if err := s.chat.System(r.Context(), fmt.Sprintf("%s refreshed the invite link", sess.Name)); err != nil {
		s.logger.Warn("post rotation system line", "err", err)
	}
	writeJSON(w, http.StatusOK, s.rooms.Links(room))
}
