package api

// The transport: what a host presses, and what every client reads.
//
// These are plain HTTP rather than data-channel commands, which is the
// simplification that comes with the server owning playback. The session
// already carries the role, so there is no participant to identify, no roster
// to consult and no race to lose — the problems the projector's data-channel
// authorisation had.

import (
	"errors"
	"net/http"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/ShadowElf37/VideoStream/server/internal/media"
	"github.com/ShadowElf37/VideoStream/server/internal/playback"
)

type playbackCommand struct {
	// Action is one of: load, enqueue, play, pause, toggle, seek, stop.
	Action string `json:"action"`
	// MediaID for load and enqueue.
	MediaID string `json:"mediaId,omitempty"`
	// PosMS for seek; relative when Relative is set.
	PosMS    int64 `json:"posMs,omitempty"`
	Relative bool  `json:"relative,omitempty"`
}

// handleGetPlayback returns the room's current playback state, for joining and
// for reconnecting. Any room member may read it.
func (s *Server) handleGetPlayback(w http.ResponseWriter, r *http.Request) {
	if s.director == nil {
		writeError(w, http.StatusNotFound, "this server has no media library")
		return
	}
	writeJSON(w, http.StatusOK, s.director.Snapshot())
}

// handlePlaybackCommand drives the room. Host-only, except pause, which the
// anyoneCanPause room setting can open up — the same rule the projector
// applies, enforced here where the role is already known.
func (s *Server) handlePlaybackCommand(w http.ResponseWriter, r *http.Request) {
	if s.director == nil {
		writeError(w, http.StatusNotFound, "this server has no media library")
		return
	}
	sess := sessionFromContext(r.Context())

	var cmd playbackCommand
	if !decodeJSON(w, r, &cmd) {
		return
	}

	if sess.Role != proto.RoleHost {
		room, err := s.rooms.Get(r.Context())
		if err != nil {
			s.logger.Error("get room failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		pauseish := cmd.Action == "pause" || cmd.Action == "play" || cmd.Action == "toggle"
		if !pauseish || !room.Settings.AnyoneCanPause {
			writeError(w, http.StatusForbidden, "host role required")
			return
		}
	}

	ctx := r.Context()
	var err error
	switch cmd.Action {
	case "load":
		err = s.director.Load(ctx, cmd.MediaID)
	case "enqueue":
		err = s.director.Enqueue(ctx, cmd.MediaID)
	case "play":
		err = s.director.SetPaused(ctx, false)
	case "pause":
		err = s.director.SetPaused(ctx, true)
	case "toggle":
		_, err = s.director.TogglePause(ctx)
	case "seek":
		_, err = s.director.Seek(ctx, cmd.PosMS, cmd.Relative)
	case "stop":
		s.director.Stop(ctx)
	default:
		writeError(w, http.StatusBadRequest, "unknown action "+cmd.Action)
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, media.ErrNotFound):
			writeError(w, http.StatusNotFound, "no such media")
		case errors.Is(err, playback.ErrNoMedia):
			writeError(w, http.StatusConflict, "nothing is loaded")
		default:
			s.logger.Error("playback command failed", "action", cmd.Action, "err", err)
			writeError(w, http.StatusInternalServerError, "could not do that")
		}
		return
	}
	writeJSON(w, http.StatusOK, s.director.Snapshot())
}
