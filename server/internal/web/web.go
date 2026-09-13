// Package web embeds and serves the built SPA (internal/web/dist), with a
// placeholder index.html until `make web` produces a real build.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
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

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		cleaned := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if cleaned == "." {
			cleaned = "index.html"
		}

		if strings.HasPrefix(cleaned, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}

		if _, err := fs.Stat(sub, cleaned); err != nil {
			// No such file: SPA fallback to index.html for client-side routing.
			r2 := new(http.Request)
			*r2 = *r
			r2.URL.Path = "/index.html"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
