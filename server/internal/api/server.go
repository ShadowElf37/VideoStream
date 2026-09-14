// Package api implements the HTTP API described in proto/README.md.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/ShadowElf37/VideoStream/server/internal/chat"
	"github.com/ShadowElf37/VideoStream/server/internal/config"
	"github.com/ShadowElf37/VideoStream/server/internal/media"
	"github.com/ShadowElf37/VideoStream/server/internal/occupancy"
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
	watcher  *occupancy.Watcher
	// pwLimiter throttles password attempts per client address, so the one
	// secret that guards the room cannot be guessed at line rate.
	pwLimiter *chat.Limiter
}

// NewServer wires up a Server with its dependencies.
func NewServer(cfg *config.Config, roomsSvc *rooms.Service, chatSvc *chat.Service, lkClient *lksdk.RoomServiceClient, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{cfg: cfg, rooms: roomsSvc, chat: chatSvc, lkClient: lkClient, logger: logger}
	s.pwLimiter = chat.NewLimiter(5, 30*time.Second)
	s.watcher = occupancy.New(lkClient, cfg.LinksRotateAfter, s.onRoomEmpty, logger)
	if cfg.MediaRoot != "" {
		s.library = media.New(cfg.MediaRoot)
		s.director = playback.New(chat.NewLiveKitBroadcaster(lkClient), &mediaResolver{cfg: cfg, lib: s.library})
	}
	return s
}

// Start runs the background loops: the director (advancing playlists and
// re-broadcasting for late joiners) and the occupancy watcher (rotating the
// invite link once everyone has left). Both live as long as ctx.
func (s *Server) Start(ctx context.Context) {
	if s.director != nil {
		// The director keeps its own copy of waitForEveryone so it never has
		// to reach back into the store on the transport path; seed it from
		// what was persisted, then let the settings handler push changes.
		if room, err := s.rooms.Get(ctx); err == nil {
			s.director.SetWaitForEveryone(ctx, room.Settings.WaitForEveryone)
		} else {
			s.logger.Warn("read settings for the director", "err", err)
		}
		go s.director.Run(ctx)
	}
	go s.watcher.Run(ctx)
}

// onRoomEmpty is what the watcher does once the room has stood empty for the
// configured grace: the invite link stops working, and the chat says so for
// whoever comes back.
func (s *Server) onRoomEmpty(ctx context.Context) {
	if _, err := s.rooms.Rotate(ctx); err != nil {
		s.logger.Error("rotate invite link after the room emptied", "err", err)
		return
	}
	s.logger.Info("room empty; invite link rotated")
	if err := s.chat.System(ctx, "Everyone left; the invite link has been refreshed."); err != nil {
		s.logger.Warn("post rotation system line", "err", err)
	}
}

// Routes builds the full HTTP handler: the JSON API plus the embedded SPA
// as a fallback, wrapped in logging and CORS middleware.
func (s *Server) Routes(spa http.Handler) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)

	// The door.
	mux.HandleFunc("GET /api/room", s.handleGetRoom)
	mux.HandleFunc("POST /api/room/token", s.handleToken)
	mux.HandleFunc("POST /api/room/logout", s.handleLogout)

	// Inside.
	mux.HandleFunc("GET /api/room/links", s.requireSession(s.handleGetLinks))
	mux.HandleFunc("POST /api/room/links/rotate", s.requireSession(s.handleRotateLinks))
	mux.HandleFunc("GET /api/room/chat", s.requireSession(s.handleGetChat))
	mux.HandleFunc("POST /api/room/chat", s.requireSession(s.handlePostChat))
	mux.HandleFunc("PATCH /api/room/settings", s.requireSession(s.handlePatchSettings))

	// The pushed media library. Listing is session-gated; the bytes are
	// behind a signed URL, because a <video src> sends no headers.
	mux.HandleFunc("GET /api/media", s.requireSession(s.handleListMedia))
	mux.HandleFunc("DELETE /api/media/{id}", s.requireSession(s.handleDeleteMedia))
	mux.HandleFunc("GET /media/{id}/{file}", s.handleMediaFile)
	mux.HandleFunc("GET /api/time", s.handleServerTime)

	// The transport. Host-gated by session, so there is no participant to
	// identify and no roster race to lose.
	mux.HandleFunc("GET /api/room/playback", s.requireSession(s.handleGetPlayback))
	mux.HandleFunc("POST /api/room/playback", s.requireSession(s.handlePlaybackCommand))
	mux.HandleFunc("POST /api/room/playback/ready", s.requireSession(s.handlePlaybackReady))

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
