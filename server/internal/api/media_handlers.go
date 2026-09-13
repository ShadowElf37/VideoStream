package api

// Serving the pushed library.
//
// Two surfaces: a session-gated JSON listing for the UI, and the media bytes
// themselves behind a signed URL, because a <video src> cannot send an
// Authorization header.

import (
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/ShadowElf37/VideoStream/server/internal/media"
)

type mediaListResponse struct {
	Items []mediaItem `json:"items"`
	// FreeBytes is what is left on the library's filesystem, so the UI can
	// warn before a push fails halfway rather than after.
	FreeBytes uint64 `json:"freeBytes"`
}

type mediaItem struct {
	proto.MediaMeta
	// URL is signed and time-limited; the client never builds one itself.
	URL string `json:"url"`
}

// handleListMedia returns the library. Any room member may list it — everyone
// in the room is about to watch whatever is chosen, and the titles are not
// secret. Only the bytes are gated, by the signature.
func (s *Server) handleListMedia(w http.ResponseWriter, r *http.Request) {
	if s.library == nil {
		writeError(w, http.StatusNotFound, "this server has no media library")
		return
	}
	items, err := s.library.List()
	if err != nil {
		s.logger.Error("listing media failed", "err", err)
		writeError(w, http.StatusInternalServerError, "could not read the media library")
		return
	}
	out := mediaListResponse{Items: make([]mediaItem, 0, len(items))}
	for _, m := range items {
		out.Items = append(out.Items, mediaItem{
			MediaMeta: m,
			URL:       media.URL(s.cfg.SessionSecret, m.ID, proto.MovieFileName, media.DefaultTTL),
		})
	}
	if free, err := media.FreeBytes(s.library.Root()); err == nil {
		out.FreeBytes = free
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDeleteMedia removes a title. Host-only: it is destructive and affects
// everyone's library, not just this room's.
func (s *Server) handleDeleteMedia(w http.ResponseWriter, r *http.Request) {
	if s.library == nil {
		writeError(w, http.StatusNotFound, "this server has no media library")
		return
	}
	sess := sessionFromContext(r.Context())
	if sess.Role != proto.RoleHost {
		writeError(w, http.StatusForbidden, "host role required")
		return
	}
	id := r.PathValue("id")
	if err := s.library.Delete(id); err != nil {
		if errors.Is(err, media.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no such media")
			return
		}
		s.logger.Error("deleting media failed", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "could not delete it")
		return
	}
	s.logger.Info("media deleted", "id", id, "by", sess.Identity)
	w.WriteHeader(http.StatusNoContent)
}

// handleMediaFile serves the bytes.
//
// http.ServeContent does the actual work and is the reason this is short: it
// implements Range, If-Range, conditional requests and 416 correctly, which is
// the whole contract a seeking <video> depends on.
func (s *Server) handleMediaFile(w http.ResponseWriter, r *http.Request) {
	if s.library == nil {
		http.NotFound(w, r)
		return
	}
	id, file := r.PathValue("id"), r.PathValue("file")

	q := r.URL.Query()
	if err := media.Verify(s.cfg.SessionSecret, id, file, q.Get("e"), q.Get("s")); err != nil {
		// Deliberately not distinguishing expired from forged: both mean
		// "ask the server for a fresh URL".
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	path, err := s.library.Path(id, file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}

	// private: the URL is a capability, so it must not land in a shared cache.
	// A long max-age is right anyway — the bytes never change, and letting the
	// browser keep them is what makes re-seeking within a film free.
	w.Header().Set("Cache-Control", "private, max-age=21600")
	if file == proto.MovieFileName {
		w.Header().Set("Content-Type", "video/mp4")
	}
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

// handleServerTime lets a client measure its offset from this server's clock,
// which is what keeps every viewer on the same frame. The round trip is the
// measurement, so the handler does as little as possible.
func (s *Server) handleServerTime(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		NowMS int64 `json:"nowMs"`
	}{time.Now().UnixMilli()})
}
