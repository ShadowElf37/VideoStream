// Package web embeds and serves the built SPA (internal/web/dist), with a
// placeholder index.html until `make web` produces a real build.
package web

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed dist
var distFS embed.FS

// Handler serves the embedded SPA: static files with cache headers
// (immutable for hashed assets under /assets/), falling back to
// index.html for any GET request that doesn't match a real file and
// doesn't target /api.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err) // the dist directory is embedded at build time; this can't fail
	}
	fileServer := http.FileServer(http.FS(sub))
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic(err)
	}
	// Serve index.html bytes directly: http.FileServer redirects any path
	// ending in "/index.html" to "./", which turns the SPA fallback for
	// /r/<id> into a redirect loop.
	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		cleaned := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if cleaned == "." || cleaned == "index.html" {
			serveIndex(w, r)
			return
		}

		if strings.HasPrefix(cleaned, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}

		if _, err := fs.Stat(sub, cleaned); err != nil {
			// No such file: SPA fallback to index.html for client-side routing.
			serveIndex(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
