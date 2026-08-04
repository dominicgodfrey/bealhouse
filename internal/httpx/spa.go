package httpx

import (
	"io/fs"
	"net/http"
	"strings"
)

// serveSPA serves the embedded Vite build, falling back to index.html so that
// client-side routes (/rooms/:slug, /admin/...) survive a hard refresh.
//
// Server-rendered <meta> and JSON-LD injection (architecture decision #3) hooks
// in here in build-order step 7; until then index.html is served verbatim.
func serveSPA(dist fs.FS) http.HandlerFunc {
	if dist == nil {
		return notBuilt
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		return notBuilt
	}

	files := http.FileServerFS(dist)

	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")

		if _, err := fs.Stat(dist, path); err != nil || path == "" {
			// Unknown path: hand it to React Router.
			r = r.Clone(r.Context())
			r.URL.Path = "/"
			path = "index.html"
		}

		// Vite fingerprints everything under /assets, so those are immutable.
		// index.html must never be cached or deploys go unnoticed.
		if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}

		files.ServeHTTP(w, r)
	}
}

func notBuilt(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error": "SPA not built; run `make web` (or `npm run build` in web/) and rebuild",
	})
}
