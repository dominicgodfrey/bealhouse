package httpx

import (
	"bytes"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// serveSPA serves the embedded Vite build, falling back to index.html so that
// client-side routes (/rooms/:slug, /admin/...) survive a hard refresh.
//
// On that fallback it writes the page's own <head> into the document first
// (decision #3, see meta.go). The SPA is one file for every address, so a
// crawler that does not run JavaScript would otherwise read the same empty
// shell for the home page, every room and the restaurant alike.
func serveSPA(dist fs.FS, meta *siteMeta) http.HandlerFunc {
	if dist == nil {
		return notBuilt
	}
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return notBuilt
	}

	files := http.FileServerFS(dist)
	shell := newShell(index)

	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")

		// A real file: an asset, the favicon, the logo. Served as it is.
		if stat, err := fs.Stat(dist, path); err == nil && path != "" && !stat.IsDir() {
			// Vite fingerprints everything under /assets, so those are
			// immutable. Nothing else built is.
			if strings.HasPrefix(path, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			files.ServeHTTP(w, r)
			return
		}

		// Everything else is a client-side route and gets the document, with
		// this route's head in it.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")

		body := shell.render(r, meta)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		// net/http drops the body of a HEAD response itself, so this is the
		// same code path for both methods the fallback answers.
		w.Write(body)
	}
}

// shell is index.html split at the point the head is written into.
//
// Split once at startup rather than searched per request: the document is a few
// hundred bytes and never changes for the life of the process, and doing the
// work here means a malformed one is a line in the startup log rather than a
// surprise on the first request.
type shell struct {
	head []byte // everything before </head>, with Vite's static <title> removed
	mid  []byte // </head> through <div id="root">
	tail []byte // everything after it
	full []byte // the document untouched, for when it could not be split
}

// staticTitle matches the placeholder <title> Vite's index.html carries.
//
// It has to go, or every page would have two: the browser takes the first and a
// crawler is entitled to take either, which is exactly the sort of thing that
// works in testing and puts "The Beal House" on all seven room results.
var staticTitle = regexp.MustCompile(`(?is)<title>.*?</title>`)

// rootElement matches the element React mounts into, which is where the
// server-rendered body goes (prerender.go). Written by hand in
// web/index.html, so the spelling is this repository's, but matched loosely
// because a build tool is entitled to reformat the document it emits.
var rootElement = regexp.MustCompile(`(?is)<div[^>]*\bid=["']root["'][^>]*>`)

func newShell(index []byte) shell {
	// Case-insensitive because the tag is the build tool's to spell, and a
	// document that cannot be split is served exactly as it is rather than
	// mangled — the site still works, it just has no server-rendered head.
	at := bytes.Index(bytes.ToLower(index), []byte("</head>"))
	if at < 0 {
		slog.Warn("index.html has no </head>; serving it without per-route metadata")
		return shell{full: index}
	}

	// The second split, at the opening tag of the element React mounts into.
	// Its absence costs the served body and nothing else: the head is the half
	// that decides what a page is called, and a document with no root element
	// is one this build tool has stopped producing.
	rest := index[at:]
	root := rootElement.FindIndex(rest)
	if root == nil {
		slog.Warn(`index.html has no <div id="root">; serving it without a rendered body`)
		return shell{
			head: staticTitle.ReplaceAll(index[:at], nil),
			mid:  rest,
		}
	}

	return shell{
		head: staticTitle.ReplaceAll(index[:at], nil),
		mid:  rest[:root[1]],
		tail: rest[root[1]:],
	}
}

func (s shell) render(r *http.Request, meta *siteMeta) []byte {
	if s.full != nil {
		return s.full
	}

	var head, body []byte
	if meta != nil {
		page := meta.forPath(r.Context(), canonicalPath(r.URL.Path))

		rendered, err := page.render()
		if err != nil {
			// The visible page does not depend on any of this. Losing the head
			// is a search-engine problem; failing the request is everybody's.
			slog.Error("rendering page metadata", "err", err, "path", r.URL.Path)
		}
		head = rendered

		// Inside <div id="root">, so React clears it on its first render and a
		// visitor never sees two copies of the page. A reader that runs no
		// JavaScript keeps it, which is the whole point (prerender.go).
		body = page.renderBody()
	}

	out := make([]byte, 0, len(s.head)+len(head)+len(s.mid)+len(body)+len(s.tail))
	out = append(out, s.head...)
	out = append(out, head...)
	out = append(out, s.mid...)
	out = append(out, body...)
	return append(out, s.tail...)
}

func notBuilt(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error": "SPA not built; run `make web` (or `npm run build` in web/) and rebuild",
	})
}
