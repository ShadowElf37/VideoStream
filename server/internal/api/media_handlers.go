package api

// Serving the pushed library.
//
// Two surfaces: a session-gated JSON listing for the UI, and the media bytes
// themselves behind a signed URL, because a <video src> cannot send an
// Authorization header.

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
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
			URL:       PlayURL(s.cfg.SessionSecret, m),
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
	if err := media.Verify(s.cfg.SessionSecret, id, q.Get("e"), q.Get("s")); err != nil {
		// Deliberately not distinguishing expired from forged: both mean
		// "ask the server for a fresh URL".
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Playlists are generated from the files already on disk rather than
	// stored, so they are answered here before anything touches the library
	// as a path.
	if strings.HasSuffix(file, ".m3u8") {
		s.servePlaylist(w, r, id, file, q.Get("e"), q.Get("s"))
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
	if strings.HasSuffix(file, ".mp4") {
		w.Header().Set("Content-Type", "video/mp4")
	}
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

// servePlaylist answers index.m3u8 (the master) and <rendition>.m3u8.
//
// Both are built on the spot from the fragmented MP4s in the title directory.
// Nothing is stored: the whole argument for byte-range HLS is that the bytes
// are already there, and writing a second copy of the media out as segments
// would give that up for nothing.
func (s *Server) servePlaylist(w http.ResponseWriter, r *http.Request, id, file, exp, sig string) {
	meta, err := s.library.Get(id)
	if err != nil || len(meta.Renditions) == 0 {
		// No renditions means the title predates them: its MP4 is not
		// fragmented and there is nothing to address. The client falls back
		// to the plain file, which is what it would have used anyway.
		http.NotFound(w, r)
		return
	}
	query := "e=" + url.QueryEscape(exp) + "&s=" + url.QueryEscape(sig)

	// Parsing is cached per file, so asking for the codec string of every
	// rendition to build the master costs one walk each, once.
	fragmentsOf := func(rend proto.Rendition) (media.Fragments, error) {
		path, err := s.library.Path(id, rend.File)
		if err != nil {
			return media.Fragments{}, err
		}
		st, err := os.Stat(path)
		if err != nil {
			return media.Fragments{}, err
		}
		return s.index.Fragments(path, st.Size(), st.ModTime())
	}

	var body string
	if file == proto.MasterPlaylistName {
		body = media.MasterPlaylist(meta, func(rend proto.Rendition) string {
			f, err := fragmentsOf(rend)
			if err != nil {
				return ""
			}
			return f.Codecs
		}, query)
	} else {
		name := strings.TrimSuffix(file, ".m3u8")
		var rend *proto.Rendition
		for i := range meta.Renditions {
			if meta.Renditions[i].Name == name {
				rend = &meta.Renditions[i]
				break
			}
		}
		if rend == nil {
			http.NotFound(w, r)
			return
		}
		frags, err := fragmentsOf(*rend)
		if err != nil {
			if !errors.Is(err, media.ErrNotFragmented) {
				s.logger.Error("reading fragments", "id", id, "file", rend.File, "err", err)
			}
			http.NotFound(w, r)
			return
		}
		body = media.MediaPlaylist(frags, rend.File, meta.DurationMS, query)
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	// The playlist is derived from files that never change, but it carries
	// the caller's signature and must not be shared.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = io.WriteString(w, body)
}

// handleServerTime lets a client measure its offset from this server's clock,
// which is what keeps every viewer on the same frame. The round trip is the
// measurement, so the handler does as little as possible.
func (s *Server) handleServerTime(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		NowMS int64 `json:"nowMs"`
	}{time.Now().UnixMilli()})
}
