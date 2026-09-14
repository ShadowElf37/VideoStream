package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ShadowElf37/VideoStream/server/internal/tokens"
)

// devOrigin is the Vite dev server origin allowed for cross-origin requests
// in local development. Production serves the SPA same-origin.
const devOrigin = "http://localhost:5173"

type contextKey int

const sessionContextKey contextKey = iota

// sessionFromContext returns the verified session stashed by requireSession.
func sessionFromContext(ctx context.Context) *tokens.Session {
	s, _ := ctx.Value(sessionContextKey).(*tokens.Session)
	return s
}

// loggingMiddleware logs each request's method, path, status and duration.
func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// corsMiddleware allows the Vite dev server origin to make credentialed
// cross-origin requests; production is same-origin and needs no CORS.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin == devOrigin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireSession verifies the Authorization: Bearer <session> header and
// stashes the verified session in the request context for handlers to use.
func (s *Server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			writeError(w, http.StatusUnauthorized, "missing bearer session token")
			return
		}
		raw := strings.TrimPrefix(header, prefix)

		sess, err := tokens.Verify(s.cfg.SessionSecret, raw)
		if err != nil {
			status := http.StatusUnauthorized
			msg := "invalid session"
			if errors.Is(err, tokens.ErrSessionExpired) {
				msg = "session expired"
			}
			writeError(w, status, msg)
			return
		}

		ctx := context.WithValue(r.Context(), sessionContextKey, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}
