// Package api implements the HTTP API described in proto/README.md.
package api

import (
	"context"
	"log/slog"
	"net/http"

	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/ShadowElf37/VideoStream/server/internal/chat"
	"github.com/ShadowElf37/VideoStream/server/internal/config"
	"github.com/ShadowElf37/VideoStream/server/internal/media"
	"github.com/ShadowElf37/VideoStream/server/internal/playback"
	"github.com/ShadowElf37/VideoStream/server/internal/rooms"
)

// Server holds the dependencies shared by HTTP handlers.
type Server struct {
	cfg      *config.Config
	rooms    *rooms.Service
	chat     *chat.Service
	lkClient *lksdk.RoomServiceClient
	logger   *slog.Logger
	library  *media.Library
	director *playback.Director
}

// NewServer wires up a Server with its dependencies.
func NewServer(cfg *config.Config, roomsSvc *rooms.Service, chatSvc *chat.Service, lkClient *lksdk.RoomServiceClient, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{cfg: cfg, rooms: roomsSvc, chat: chatSvc, lkClient: lkClient, logger: logger}
	if cfg.MediaRoot != "" {
		s.library = media.New(cfg.MediaRoot)
		s.director = playback.New(chat.NewLiveKitBroadcaster(lkClient), &mediaResolver{cfg: cfg, lib: s.library})
	}
	return s
}

// StartDirector runs the playback loop, which advances playlists when a title
// ends and re-broadcasts periodically so late joiners converge without asking.
// A no-op when there is no media library.
func (s *Server) StartDirector(ctx context.Context) {
	if s.director != nil {
		go s.director.Run(ctx)
	}
}

// Routes builds the full HTTP handler: the JSON API plus the embedded SPA
// as a fallback, wrapped in logging and CORS middleware.
func (s *Server) Routes(spa http.Handler) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)

	mux.HandleFunc("POST /api/rooms", s.handleCreateRoom)
	mux.HandleFunc("GET /api/rooms/{id}", s.handleGetRoom)
	mux.HandleFunc("POST /api/rooms/{id}/token", s.handleToken)
	mux.HandleFunc("GET /api/rooms/{id}/chat", s.requireSession(s.handleGetChat))
	mux.HandleFunc("POST /api/rooms/{id}/chat", s.requireSession(s.handlePostChat))
	mux.HandleFunc("PATCH /api/rooms/{id}/settings", s.requireSession(s.handlePatchSettings))

	// The pushed media library. Listing is session-gated; the bytes are
	// behind a signed URL, because a <video src> sends no headers.
	mux.HandleFunc("GET /api/media", s.requireAnySession(s.handleListMedia))
	mux.HandleFunc("DELETE /api/media/{id}", s.requireAnySession(s.handleDeleteMedia))
	mux.HandleFunc("GET /media/{id}/{file}", s.handleMediaFile)
	mux.HandleFunc("GET /api/time", s.handleServerTime)

	// The transport. Host-gated by session, so there is no participant to
	// identify and no roster race to lose.
	mux.HandleFunc("GET /api/rooms/{id}/playback", s.requireSession(s.handleGetPlayback))
	mux.HandleFunc("POST /api/rooms/{id}/playback", s.requireSession(s.handlePlaybackCommand))

	if spa != nil {
		mux.Handle("/", spa)
	}

	return corsMiddleware(loggingMiddleware(s.logger, mux))
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
