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
	"github.com/ShadowElf37/VideoStream/server/internal/tokens"
)

type playbackCommand struct {
	// Action is one of: load, enqueue, play, pause, toggle, seek, stop, start.
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

// actorOf is the session as the director's notion of who did something.
func actorOf(sess *tokens.Session) proto.PlaybackActor {
	if sess == nil {
		return proto.PlaybackActor{}
	}
	return proto.PlaybackActor{Identity: sess.Identity, Name: sess.Name, Color: sess.Color}
}

// handlePlaybackReady takes a client's word on whether it could start now.
//
// Unauthenticated by role on purpose: every person in the room reports, and
// the session is what names them. A report is advice, never a command — the
// worst a lying client can do is make the room wait out the hold timeout,
// which it could do by simply buffering slowly anyway.
func (s *Server) handlePlaybackReady(w http.ResponseWriter, r *http.Request) {
	if s.director == nil {
		writeError(w, http.StatusNotFound, "this server has no media library")
		return
	}
	sess := sessionFromContext(r.Context())
	var body proto.PlaybackReady
	if !decodeJSON(w, r, &body) {
		return
	}
	s.director.Report(r.Context(), sess.Identity, sess.Name, body)
	w.WriteHeader(http.StatusNoContent)
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
	// Who asked. The director echoes it on `playback.intent` ahead of the
	// state, so a viewer sees "Alice skipped to 1:12:30" rather than the
	// picture simply jumping.
	actor := actorOf(sess)
	var err error
	switch cmd.Action {
	case "load":
		err = s.director.Load(ctx, actor, cmd.MediaID)
	case "enqueue":
		err = s.director.Enqueue(ctx, actor, cmd.MediaID)
	case "play":
		err = s.director.SetPaused(ctx, actor, false)
	case "pause":
		err = s.director.SetPaused(ctx, actor, true)
	case "toggle":
		_, err = s.director.TogglePause(ctx, actor)
	case "seek":
		_, err = s.director.Seek(ctx, actor, cmd.PosMS, cmd.Relative)
	case "stop":
		s.director.Stop(ctx, actor)
	case "start":
		// The override for waitForEveryone: go now, whoever is still
		// buffering. Host-only even with anyoneCanPause, because deciding to
		// leave someone behind is not a pause.
		err = s.director.Start(ctx)
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
