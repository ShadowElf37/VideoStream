// Package api implements the HTTP API described in proto/README.md.
package api

import (
	"log/slog"
	"net/http"

	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/ShadowElf37/VideoStream/server/internal/chat"
	"github.com/ShadowElf37/VideoStream/server/internal/config"
	"github.com/ShadowElf37/VideoStream/server/internal/rooms"
)

// Server holds the dependencies shared by HTTP handlers.
type Server struct {
	cfg      *config.Config
	rooms    *rooms.Service
	chat     *chat.Service
	lkClient *lksdk.RoomServiceClient
	logger   *slog.Logger
	house    houseState
}

// NewServer wires up a Server with its dependencies.
func NewServer(cfg *config.Config, roomsSvc *rooms.Service, chatSvc *chat.Service, lkClient *lksdk.RoomServiceClient, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{cfg: cfg, rooms: roomsSvc, chat: chatSvc, lkClient: lkClient, logger: logger}
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

	// The house projector: polled by the projector service itself, and
	// switched on and off by the room's host.
	mux.HandleFunc("GET /api/house/assignment", s.handleHouseAssignment)
	mux.HandleFunc("GET /api/rooms/{id}/projector", s.handleGetHouseProjector)
	mux.HandleFunc("POST /api/rooms/{id}/projector", s.requireSession(s.handleSetHouseProjector))
	mux.HandleFunc("DELETE /api/rooms/{id}/projector", s.requireSession(s.handleSetHouseProjector))

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
