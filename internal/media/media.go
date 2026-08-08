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
// # Widths first, then formats (decision #16)
//
// An upload produces a ladder: the canonical image at up to [maxEdge], and a
// copy at each standard width below it, in JPEG and in WebP. The page picks
// with `srcset`.
//
// **The widths matter more than the formats, by a lot.** Measured on a
// 2400×1600 photograph, the 960px JPEG is 76 KB against 955 KB for the 2400px
// one — twelve times — where WebP at the same width saves a further 50% and
// AVIF about 72%. A phone rendering a room card four hundred CSS pixels wide
// was downloading the full-size image, and no amount of format cleverness
// recovers that. So the ladder is the change and WebP rides along on it.
//
// **AVIF is deliberately not here.** It is feasible — the same family of
// WASM-backed encoders provides it, with no cgo — and it was measured at
// −61% against JPEG at full size. What it costs is 5.3 MB of binary and
// roughly 1.7 seconds of encoding per upload, and that second number is what
// decides it: it would push this work off the request and into a background
// job, which then needs the API to report which variants exist yet so that a
// `<picture>` never points at a file that has not been written. The whole
// ladder in JPEG and WebP takes about half a second and needs none of that.
// Adding AVIF later is still a job that walks the directory.
//
// **The encoder is `gen2brain/webp`, chosen for one property**: it is libwebp
// compiled to WebAssembly and run under wazero, so it builds with `CGO_ENABLED=0`
// on a machine with no C compiler — which is this one, and is also why
// `go test -race` does not work here. The pure-Go alternatives encode lossless
// VP8L only, which for a photograph is larger than the JPEG it replaces.
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
//
// The ladder is named *from* that hash rather than each rung being hashed
// separately: `<hash>-w2400.jpg` is the canonical file and `<hash>-w960.webp`
// is one of its rungs. Two consequences. The rungs are only as immutable as
// the encoder settings are stable, so **changing [jpegQuality], [webpQuality]
// or the ladder means new canonical names, not new bytes at old ones** — which
// is what a change to [maxEdge] gets you for free and what a quality change
// does not. And the width is *in the name*, so which rungs exist is knowable
// from the stored path alone, with no directory listing and no column: see
// [Sources]. That is what keeps `srcset` from ever naming a file that is not
// there, which does not degrade — it renders as a broken image.
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
	"strconv"
	"strings"

	"github.com/gen2brain/webp"
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

// webpQuality is lower than the JPEG's on purpose: WebP at 80 is visually the
// equal of JPEG at 85 and about a third smaller. Matching the numbers would
// throw away most of what the format is for.
const webpQuality = 80

// ladder is the widths a photograph is stored at, smallest first.
//
// Four rungs roughly doubling: a phone showing a card, a phone showing the room
// page, a laptop, and a high-density desktop. More rungs would be encoding work
// and disk for differences no eye resolves; fewer means somebody downloads
// twice what their screen can show. The top rung is [maxEdge], so the canonical
// file is always the last one.
var ladder = [...]int{480, 960, 1600, maxEdge}

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
// and stores it — and every smaller rung of the ladder, in JPEG and WebP —
// under names derived from its contents.
//
// The returned path is the canonical JPEG: what goes in the database, what an
// `<img src>` points at, and the one every browser can render whatever it makes
// of the rest. [Sources] derives the others from it.
func (s *Store) Save(r io.Reader) (string, error) {
	// Bounded before it is decoded, not after. An image bomb is a small file
	// that decodes to something enormous, so the limit on the way in is the only
	// one that helps — and image.Decode below allocates from what it reads.
	limited := io.LimitReader(r, MaxUpload+1)

	source, _, err := image.Decode(limited)
	if err != nil {
		return "", ErrNotAnImage
	}

	canonical := fit(source)
	encoded, err := encodeJPEG(canonical)
	if err != nil {
		return "", err
	}

	// The hash is of the canonical JPEG, as it always was, so the same
	// photograph uploaded twice is still one set of files and an upload that
	// has already been stored costs nothing below.
	sum := sha256.Sum256(encoded)
	stem := hex.EncodeToString(sum[:16])
	width := canonical.Bounds().Dx()

	if err := s.write(rung(stem, width, ".jpg"), encoded); err != nil {
		return "", err
	}

	// Everything below the canonical rung, plus the canonical width in WebP.
	// Scaling from `canonical` rather than from `source` for the same reason
	// the page does not: it is already the right colours and a fraction of the
	// pixels, and nobody can see the difference between one resample and two at
	// these ratios.
	for _, w := range widths(width) {
		img := canonical
		if w != width {
			img = resize(canonical, w)
		}

		if w != width { // the canonical JPEG is already written
			jpg, err := encodeJPEG(img)
			if err != nil {
				return "", err
			}
			if err := s.write(rung(stem, w, ".jpg"), jpg); err != nil {
				return "", err
			}
		}

		wp, err := encodeWebP(img)
		if err != nil {
			return "", err
		}
		if err := s.write(rung(stem, w, ".webp"), wp); err != nil {
			return "", err
		}
	}

	return URLPrefix + rung(stem, width, ".jpg"), nil
}

// rung composes a name. One function, because the writer and [Sources] have to
// agree exactly and a format string in each is how they stop agreeing.
func rung(stem string, width int, ext string) string {
	return stem + "-w" + strconv.Itoa(width) + ext
}

// widths is the ladder for an image of this width: every standard rung below
// it, and the width itself.
//
// Never a rung above, because upscaling makes a bigger file that looks worse,
// and never a rung within a hair of the canonical width, because two files
// nobody can tell apart is disk and encoding time for nothing.
func widths(canonical int) []int {
	out := make([]int, 0, len(ladder))
	for _, w := range ladder {
		if w < canonical*9/10 {
			out = append(out, w)
		}
	}
	return append(out, canonical)
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
	return scale(source, width, height)
}

// resize scales to an exact width, keeping the aspect ratio.
func resize(source image.Image, width int) image.Image {
	bounds := source.Bounds()
	height := bounds.Dy() * width / bounds.Dx()
	if height < 1 {
		height = 1
	}
	return scale(source, width, height)
}

func scale(source image.Image, width, height int) image.Image {
	// CatmullRom rather than the cheaper kernels: this runs once per upload on a
	// two-core box and the result is looked at for years.
	out := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(out, out.Bounds(), source, source.Bounds(), draw.Src, nil)
	return out
}

func encodeJPEG(img image.Image) ([]byte, error) {
	var buf writerTo
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, fmt.Errorf("media: encoding the image: %w", err)
	}
	return buf.bytes, nil
}

func encodeWebP(img image.Image) ([]byte, error) {
	var buf writerTo
	if err := webp.Encode(&buf, img, webp.Options{Quality: webpQuality}); err != nil {
		return nil, fmt.Errorf("media: encoding WebP: %w", err)
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

// Ladder is the set of sizes one stored photograph is available at, ready for
// `srcset`.
//
// Both strings are empty for a path with no ladder — one stored before this
// existed, or one from somewhere else entirely — and a page with an empty
// srcset renders the plain `<img src>`, which is the canonical JPEG and works
// everywhere. That is why the fallback is a real file and not a rung.
type Ladder struct {
	// JPEG and WebP are srcset attribute values: "url 480w, url 960w, …".
	JPEG string `json:"srcset,omitempty"`
	WebP string `json:"webpSrcset,omitempty"`
}

// Sources derives a photograph's ladder from its stored path.
//
// **Pure, and deliberately so.** It reads no directory and takes no Store, so
// the handlers that assemble room cards and search results can call it without
// one being threaded through three packages — and there is one implementation
// of the rule rather than a second copy of it in TypeScript that drifts. The
// width is in the canonical file's own name, which is what makes that possible:
// every rung the writer produced is recoverable from the name alone, so the
// srcset can never advertise a file that was not written. That failure does not
// degrade; a 404 inside a srcset is a broken image.
func Sources(stored string) Ladder {
	name := Name(stored)
	if name == "" {
		return Ladder{}
	}
	stem, width, ok := split(name)
	if !ok {
		// A path from before the ladder existed. The file is real and the page
		// will show it; it simply has no other sizes to offer.
		return Ladder{}
	}

	all := widths(width)
	if len(all) < 2 {
		// One rung is not a choice, and a one-entry srcset only adds bytes to
		// the HTML. The WebP is still worth advertising.
		return Ladder{WebP: srcset(stem, all, ".webp")}
	}
	return Ladder{
		JPEG: srcset(stem, all, ".jpg"),
		WebP: srcset(stem, all, ".webp"),
	}
}

func srcset(stem string, widths []int, ext string) string {
	var b strings.Builder
	for i, w := range widths {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(URLPrefix + rung(stem, w, ext) + " " + strconv.Itoa(w) + "w")
	}
	return b.String()
}

// split takes "<stem>-w960.jpg" apart. The inverse of rung, and false for
// anything that does not have that shape.
func split(name string) (stem string, width int, ok bool) {
	base := strings.TrimSuffix(name, path.Ext(name))
	at := strings.LastIndex(base, "-w")
	if at < 1 {
		return "", 0, false
	}
	width, err := strconv.Atoi(base[at+2:])
	if err != nil || width < 1 {
		return "", 0, false
	}
	return base[:at], width, true
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
