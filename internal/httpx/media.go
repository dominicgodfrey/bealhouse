package httpx

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"bealhouse/internal/media"
)

// Uploading a photograph, and serving it back (decision #16).
//
// The two halves are here together because they have to agree about one string:
// the path stored in `room_photos.path` is produced by the first and resolved by
// the second, and a literal in each is how they stop agreeing.

// uploadPhoto takes one image from the console and returns where it now lives.
//
// Multipart rather than JSON, because the alternative is base64 in a request
// body — a third again as many bytes over a phone connection, for a file that is
// already several megabytes. See uploadsAreSameSite for what that costs and why
// it is still safe.
//
// It returns a path and nothing else. Attaching it to a room is a separate save,
// so an upload that the owner then changes their mind about leaves a file nobody
// references rather than a half-edited room.
func uploadPhoto(store *media.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The multipart reader buffers to disk past this, so it bounds memory
		// rather than the upload; media.MaxUpload below is what bounds the file.
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			badRequest(w, "that upload could not be read")
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()

		file, header, err := r.FormFile("photo")
		if err != nil {
			badRequest(w, "no file was attached")
			return
		}
		defer file.Close()

		if header.Size > media.MaxUpload {
			badRequest(w, "that photograph is too large; 25 MB is the limit")
			return
		}

		// Decoded rather than trusted: the file's name and the browser's claimed
		// content type are both the caller's to invent, and neither says whether
		// the bytes are an image. media.Save decodes them, which is what makes
		// this the check rather than a guess.
		path, err := store.Save(file)
		if errors.Is(err, media.ErrNotAnImage) {
			badRequest(w, "that file is not an image we can read; JPEG, PNG, GIF and WebP all work")
			return
		}
		if err != nil {
			serverError(w, r, err)
			return
		}

		writeJSON(w, http.StatusCreated, map[string]string{"path": path})
	}
}

// serveMedia hands back an uploaded photograph.
//
// Public, because these are pictures of the inn on the marketing site. Read-only
// and by exact name: the path is resolved through media.Name, which refuses
// anything containing a separator, so nothing that ends up in the database can
// address a file outside the media directory.
//
// Cached forever, which the content-addressed names earn: the bytes at a given
// URL cannot change, so a browser or a CDN in front of this has nothing to
// revalidate. A photograph the owner replaces gets a different name and a
// different URL.
func serveMedia(store *media.Store) http.HandlerFunc {
	files := http.FileServerFS(os.DirFS(store.Dir()))

	return func(w http.ResponseWriter, r *http.Request) {
		name := media.Name(r.URL.Path)
		if name == "" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")

		// Rewritten to the bare name so the file server, which is rooted at the
		// media directory, resolves it there and nowhere else.
		r = r.Clone(r.Context())
		r.URL.Path = "/" + name
		files.ServeHTTP(w, r)
	}
}

// mediaUnavailable answers when no media directory could be prepared.
//
// Registered rather than absent for the reason the admin routes are: a console
// whose upload button 404s tells the owner nothing, and a marketing page whose
// photographs fall through to the SPA would serve index.html to an <img> and
// render a broken picture with a 200 beside it.
func mediaUnavailable(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error": "photograph storage is not configured on this deployment",
	})
}

// uploadsAreSameSite is sameSiteOnly's rule for the one route that cannot send
// JSON.
//
// sameSiteOnly requires a JSON content type on every write, because an HTML form
// cannot produce one and a form is the single cross-origin shape that needs no
// preflight. A file upload is the exception that proves it: `multipart/form-data`
// is exactly what a form does produce, so that half of the check cannot apply
// here.
//
// What is left is Sec-Fetch-Site, and it is the stronger half. The browser sets
// it and no page script can, so a request announcing itself as cross-site is
// refused outright — and unlike the content-type rule, that holds for a form
// post as much as for anything else. What the exception actually costs is the
// belt-and-braces on a browser too old to send the header at all, against an
// endpoint whose worst outcome is a stranger putting a JPEG on the disk of a
// server they cannot then attach it to anything on.
func uploadsAreSameSite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "cross-site requests are not accepted here",
			})
			return
		}

		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/form-data") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{
				"error": "this endpoint takes a multipart upload",
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}
