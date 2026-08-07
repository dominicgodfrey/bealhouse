// Package media stores the photographs the owner uploads (decision #16).
//
// # What it does, and what it deliberately does not
//
// An image arrives from a phone in the admin console. It is decoded, scaled down
// if it is enormous, re-encoded as JPEG, and written to a directory on the VPS
// under a name derived from its own contents. The path that comes back goes in
// `room_photos.path` or `event_photos.path`, and the file is served from
// `/media/…` by the same binary.
//
// **Re-encoding is not optional polish.** A photograph off a modern phone is
// four thousand pixels wide and several megabytes, and a marketing site that
// serves those to somebody on mountain mobile data has failed at the one job the
// page has. Decoding it here also means a file that is not really an image is
// refused at the door rather than stored and served back to visitors.
//
// **What is still missing from decision #16 is the AVIF and WebP variants.**
// Go's standard library encodes JPEG, PNG and GIF; it decodes WebP through
// golang.org/x/image and encodes neither WebP nor AVIF. Producing them needs
// either cgo and libvips — which this project cannot build on the developer's
// machine, there being no C compiler — or a third-party pure-Go encoder. That is
// a dependency decision worth making deliberately rather than in passing, so
// what ships is one well-sized JPEG per photograph. The paths are stored, not
// the variants, so adding them later is a job that walks the directory rather
// than a schema change.
//
// # Content addressing
//
// A file is named for the SHA-256 of the bytes actually written. Three things
// follow, and all three are the reason it is done this way:
//
//   - The same photograph uploaded twice is one file, not two. Owners re-upload
//     constantly.
//   - The name cannot collide, so two people uploading at once cannot overwrite
//     each other, and no counter or lock is needed to allocate one.
//   - The content at a URL never changes, so the files can be served
//     `immutable` and cached forever by the browser and by Cloudflare in front
//     of it. A name derived from the upload's own filename could not be.
//
// The corollary is that **removing a photo from a room does not delete the
// file**. Two rooms may legitimately point at the same bytes, and the cost of an
// orphan is a few hundred kilobytes on a 40 GB disk against the cost of the
// alternative, which is a delete that takes a photograph off a page somebody
// else was still using.
package media

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"

	// Registered for their side effect: the decoders the image package consults
	// when it sniffs a file. GIF and PNG come from the standard library; WebP is
	// decode-only and comes from x/image, which is worth having because a phone
	// or a Mac will hand over a WebP without being asked.
	_ "image/gif"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// URLPrefix is where uploaded files are served from. One constant, because the
// route that serves them and the path stored in the database have to agree and
// a literal in each is how they stop agreeing.
const URLPrefix = "/media/"

// MaxUpload bounds what will be read from a request.
//
// Generous enough for a photograph straight off a phone, including the ones
// modern cameras produce, and far short of what would let somebody fill the disk
// with a single request.
const MaxUpload = 25 << 20

// maxEdge is the longest side an image is stored at.
//
// A little over twice the widest column the site renders, so a photograph still
// looks right on a high-density display, and nowhere near the 4000px a phone
// hands over. This is the number that decides whether the rooms page is pleasant
// on a phone in a valley.
const maxEdge = 2400

// jpegQuality is a deliberate compromise. 85 is the point above which the files
// grow noticeably and nobody can see the difference.
const jpegQuality = 85

// ErrNotAnImage is a file that could not be decoded as one.
//
// Its own error because it is the owner's mistake and not the server's: they
// picked a PDF, or a photograph the phone had not finished writing. The console
// says so and lets them try again.
var ErrNotAnImage = errors.New("media: that file is not an image we can read")

// Store is a directory of uploaded files.
type Store struct{ dir string }

// New prepares the directory, creating it if it is not there.
//
// Failing here is a reason to refuse to start: a console whose upload button
// fails on every press is worse than a server that says why at boot.
func New(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("media: no directory configured")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("media: preparing %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// Dir is where the files are, for the handler that serves them.
func (s *Store) Dir() string { return s.dir }

// Save decodes an uploaded image, scales it to something a web page can carry,
// and stores it under a name derived from its contents.
//
// The returned path is what goes in the database and what the browser asks for.
func (s *Store) Save(r io.Reader) (string, error) {
	// Bounded before it is decoded, not after. An image bomb is a small file
	// that decodes to something enormous, so the limit on the way in is the only
	// one that helps — and image.Decode below allocates from what it reads.
	limited := io.LimitReader(r, MaxUpload+1)

	source, _, err := image.Decode(limited)
	if err != nil {
		return "", ErrNotAnImage
	}

	encoded, err := encode(fit(source))
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(encoded)
	name := hex.EncodeToString(sum[:16]) + ".jpg"

	if err := s.write(name, encoded); err != nil {
		return "", err
	}
	return URLPrefix + name, nil
}

// write puts the bytes at name, and is a no-op if they are already there.
//
// Through a temporary file and a rename, so a crash or a second uploader mid-
// write cannot leave a half-written image being served. Rename is atomic within
// a directory on every filesystem this runs on.
func (s *Store) write(name string, content []byte) error {
	final := filepath.Join(s.dir, name)

	// Content-addressed, so an existing file with this name already holds
	// exactly these bytes. Rewriting it would be work for no change.
	if _, err := os.Stat(final); err == nil {
		return nil
	}

	tmp, err := os.CreateTemp(s.dir, ".upload-*")
	if err != nil {
		return fmt.Errorf("media: creating a temporary file: %w", err)
	}
	defer func() {
		tmp.Close()
		// Harmless once the rename has happened; the only thing it can remove
		// after that is a temporary file the rename left behind on failure.
		_ = os.Remove(tmp.Name())
	}()

	if _, err := tmp.Write(content); err != nil {
		return fmt.Errorf("media: writing the upload: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("media: closing the upload: %w", err)
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return fmt.Errorf("media: storing the upload: %w", err)
	}
	return nil
}

// fit scales an image down so its longest side is at most maxEdge, and leaves
// anything already smaller alone.
//
// Only ever down. Enlarging a small photograph makes a bigger file that looks
// worse, and the page would rather have the small one.
func fit(source image.Image) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	longest := width
	if height > longest {
		longest = height
	}
	if longest <= maxEdge {
		return source
	}

	width = width * maxEdge / longest
	height = height * maxEdge / longest

	// CatmullRom rather than the cheaper kernels: this runs once per upload on a
	// two-core box and the result is looked at for years.
	out := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(out, out.Bounds(), source, bounds, draw.Src, nil)
	return out
}

func encode(img image.Image) ([]byte, error) {
	var buf writerTo
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("media: encoding the image: %w", err)
	}
	return buf.bytes, nil
}

// writerTo is a minimal bytes.Buffer. jpeg.Encode needs an io.Writer and this
// avoids a second copy of the encoded image on the way out of one.
type writerTo struct{ bytes []byte }

func (w *writerTo) Write(p []byte) (int, error) {
	w.bytes = append(w.bytes, p...)
	return len(p), nil
}

// Name extracts the stored filename from a path this package produced.
//
// Returns "" for anything that is not one — a path from somewhere else, or one
// with a directory separator in it. The serving handler uses this so a stored
// path can never reach outside the media directory, whatever ends up in the
// database.
func Name(stored string) string {
	if !strings.HasPrefix(stored, URLPrefix) {
		return ""
	}
	name := strings.TrimPrefix(stored, URLPrefix)
	if name == "" || name != path.Base(name) || strings.HasPrefix(name, ".") {
		return ""
	}
	return name
}
