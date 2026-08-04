// Package web embeds the built Vite bundle into the Go binary so that a single
// artifact serves both the API and the SPA.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built SPA rooted at dist/. It is empty until `npm run build`
// has run; callers should use Built to decide whether to serve it.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// Only possible if the embed directive above is broken, which is a
		// compile-time concern, not a runtime one.
		panic(err)
	}
	return sub
}

// Built reports whether a real Vite build was embedded, as opposed to the
// placeholder that keeps dist/ present in git.
func Built() bool {
	_, err := fs.Stat(Dist(), "index.html")
	return err == nil
}
