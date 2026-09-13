// Package config loads server configuration from environment variables.
package config

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
)

// Config holds all runtime configuration for the app server.
type Config struct {
	Listen    string
	PublicURL string

	// LiveKitURL is the public websocket URL handed to clients.
	LiveKitURL string
	// LiveKitAPIURL is the internal HTTP URL used for server-side LiveKit API calls.
	LiveKitAPIURL    string
	LiveKitAPIKey    string
	LiveKitAPISecret string

	// SessionSecret is the HMAC key used to sign opaque session tokens.
	SessionSecret []byte

	DBPath string

	// MediaRoot is the directory of pushed titles (see internal/media).
	// Empty disables the library, and the UI stops offering it.
	MediaRoot string
}

// Load reads configuration from the environment, applying defaults and
// validating required values. It never exits the process; callers decide
// how to handle a returned error.
func Load() (*Config, error) {
	cfg := &Config{
		Listen:           getEnv("LISTEN", ":8080"),
		PublicURL:        getEnv("PUBLIC_URL", "http://localhost:8080"),
		LiveKitAPIURL:    getEnv("LIVEKIT_API_URL", "http://localhost:7880"),
		LiveKitAPIKey:    os.Getenv("LIVEKIT_API_KEY"),
		LiveKitAPISecret: os.Getenv("LIVEKIT_API_SECRET"),
		DBPath:           getEnv("DB_PATH", "./data/videostream.db"),
		MediaRoot:        os.Getenv("MEDIA_ROOT"),
	}

	if cfg.LiveKitAPIKey == "" || cfg.LiveKitAPISecret == "" {
		return nil, errors.New("LIVEKIT_API_KEY and LIVEKIT_API_SECRET are required")
	}

	cfg.LiveKitURL = os.Getenv("LIVEKIT_URL")
	if cfg.LiveKitURL == "" {
		cfg.LiveKitURL = defaultLiveKitURL(cfg.PublicURL)
	}

	if secret := os.Getenv("SESSION_SECRET"); secret != "" {
		cfg.SessionSecret = []byte(secret)
	} else {
		random, err := randomSecret(32)
		if err != nil {
			return nil, fmt.Errorf("generate random session secret: %w", err)
		}
		cfg.SessionSecret = random
		slog.Warn("SESSION_SECRET not set; generated a random secret for this process; sessions will not survive a restart")
	}

	return cfg, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func randomSecret(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// defaultLiveKitURL derives the public LiveKit websocket URL from PUBLIC_URL
// when LIVEKIT_URL isn't set explicitly: localhost keeps the default dev
// port, everything else gets its scheme swapped to wss.
func defaultLiveKitURL(publicURL string) string {
	u, err := url.Parse(publicURL)
	if err != nil || u.Host == "" {
		return "ws://localhost:7880"
	}
	hostname := u.Hostname()
	if u.Scheme == "http" && (hostname == "localhost" || hostname == "127.0.0.1") {
		return "ws://localhost:7880"
	}
	return "wss://" + strings.TrimSuffix(u.Host, "/")
}
