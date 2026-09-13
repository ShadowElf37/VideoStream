// Command server runs the VideoStream app server: room/token/chat/settings
// HTTP API plus the embedded SPA, backed by SQLite and LiveKit.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/ShadowElf37/VideoStream/server/internal/api"
	"github.com/ShadowElf37/VideoStream/server/internal/chat"
	"github.com/ShadowElf37/VideoStream/server/internal/config"
	"github.com/ShadowElf37/VideoStream/server/internal/rooms"
	"github.com/ShadowElf37/VideoStream/server/internal/store"
	"github.com/ShadowElf37/VideoStream/server/internal/web"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	lkClient := lksdk.NewRoomServiceClient(cfg.LiveKitAPIURL, cfg.LiveKitAPIKey, cfg.LiveKitAPISecret)
	broadcaster := chat.NewLiveKitBroadcaster(lkClient)

	roomsSvc := rooms.NewService(st, cfg)
	chatSvc := chat.NewService(st, broadcaster)

	srv := api.NewServer(cfg, roomsSvc, chatSvc, lkClient, logger)
	handler := srv.Routes(web.Handler())

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Listen, "public_url", cfg.PublicURL, "livekit_url", cfg.LiveKitURL)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	case err := <-errCh:
		return err
	}
}
