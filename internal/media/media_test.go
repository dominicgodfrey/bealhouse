package media

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A photograph straight off a phone is four thousand pixels wide and several
// megabytes. Serving that to somebody on mountain mobile data is the failure
// this whole step exists to avoid, so the scaling is the behaviour worth
// pinning: not that it happens, but that the longest side lands on the limit
// and the shape is kept.
func TestALargePhotographIsScaledDown(t *testing.T) {
	store := newStore(t)

	stored, err := store.Save(bytes.NewReader(pngOf(t, 3600, 2400)))
	if err != nil {
		t.Fatalf("saving: %v", err)
	}

	bounds := decodeStored(t, store, stored).Bounds()
	if bounds.Dx() != maxEdge {
		t.Errorf("width is %d, want the longest side at %d", bounds.Dx(), maxEdge)
	}
	// 3600x2400 is 3:2, so the short side follows.
	if want := maxEdge * 2400 / 3600; bounds.Dy() != want {
		t.Errorf("height is %d, want %d — the aspect ratio was not kept", bounds.Dy(), want)
	}
}

// Enlarging a small photograph makes a bigger file that looks worse. The page
// would rather have the small one.
func TestASmallPhotographIsLeftAlone(t *testing.T) {
	store := newStore(t)

	stored, err := store.Save(bytes.NewReader(pngOf(t, 800, 600)))
	if err != nil {
		t.Fatalf("saving: %v", err)
	}

	bounds := decodeStored(t, store, stored).Bounds()
	if bounds.Dx() != 800 || bounds.Dy() != 600 {
		t.Errorf("stored at %dx%d, want the original 800x600", bounds.Dx(), bounds.Dy())
	}
}

// The same photograph uploaded twice is one file.
//
// Owners re-upload constantly — the picture they meant, after the one they did
// not — and the content-addressed name is what stops the disk filling with
// copies. It is also what makes the URL safe to cache forever, since the bytes
// at a given name can never change.
func TestTheSamePhotographStoresOnce(t *testing.T) {
	store := newStore(t)
	original := pngOf(t, 1200, 900)

	first, err := store.Save(bytes.NewReader(original))
	if err != nil {
		t.Fatalf("saving: %v", err)
	}
	second, err := store.Save(bytes.NewReader(original))
	if err != nil {
		t.Fatalf("saving again: %v", err)
	}

	if first != second {
		t.Errorf("the same image stored at two paths: %q and %q", first, second)
	}

	// And nothing is left lying about: the temporary file the write goes
	// through is removed whether or not the rename happened.
	entries, err := os.ReadDir(store.Dir())
	if err != nil {
		t.Fatalf("reading the store: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("%d files in the store (%v), want exactly 1", len(entries), names)
	}
}

// A different photograph is a different file, which is the other half of the
// name meaning anything.
func TestADifferentPhotographStoresSeparately(t *testing.T) {
	store := newStore(t)

	first, err := store.Save(bytes.NewReader(pngOf(t, 400, 400)))
	if err != nil {
		t.Fatalf("saving: %v", err)
	}
	second, err := store.Save(bytes.NewReader(pngOf(t, 401, 400)))
	if err != nil {
		t.Fatalf("saving: %v", err)
	}

	if first == second {
		t.Error("two different images stored at the same path")
	}
}

// The file's name and the browser's claimed content type are both the caller's
// to invent. Decoding is what actually says whether the bytes are an image, and
// it is the reason a PDF renamed to .jpg does not end up being served to
// visitors as one.
func TestSomethingThatIsNotAnImageIsRefused(t *testing.T) {
	store := newStore(t)

	_, err := store.Save(strings.NewReader("%PDF-1.7\nnot a photograph at all"))
	if err != ErrNotAnImage {
		t.Fatalf("err = %v, want ErrNotAnImage", err)
	}

	entries, _ := os.ReadDir(store.Dir())
	if len(entries) != 0 {
		t.Errorf("%d files written for a rejected upload, want none", len(entries))
	}
}

// Name is what stands between whatever ends up in the database and the
// filesystem. Anything with a separator in it, anything hidden, anything not
// under the prefix: no name, and the handler 404s rather than reading it.
func TestNameRefusesAnythingThatCouldEscapeTheDirectory(t *testing.T) {
	refused := []string{
		"/media/../../etc/passwd",
		"/media/../secrets.env",
		"/media/nested/file.jpg",
		"/media/",
		"/media/.hidden",
		"/photos/elsewhere.jpg",
		"",
		"../../etc/passwd",
	}

	for _, path := range refused {
		if got := Name(path); got != "" {
			t.Errorf("Name(%q) = %q, want it refused", path, got)
		}
	}

	if got := Name("/media/abc123.jpg"); got != "abc123.jpg" {
		t.Errorf("Name of an ordinary stored path = %q", got)
	}
}

// ---------------------------------------------------------------------------

func newStore(t *testing.T) *Store {
	t.Helper()
	store, err := New(filepath.Join(t.TempDir(), "media"))
	if err != nil {
		t.Fatalf("preparing the store: %v", err)
	}
	return store
}

// pngOf builds an image with content that varies by pixel, so two of different
// sizes cannot encode to the same bytes.
func pngOf(t *testing.T, width, height int) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), uint8((x + y) % 256), 255})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("building a test image: %v", err)
	}
	return buf.Bytes()
}

func decodeStored(t *testing.T, store *Store, storedPath string) image.Image {
	t.Helper()

	name := Name(storedPath)
	if name == "" {
		t.Fatalf("Save returned %q, which Name does not accept", storedPath)
	}

	file, err := os.Open(filepath.Join(store.Dir(), name))
	if err != nil {
		t.Fatalf("opening what was stored: %v", err)
	}
	defer file.Close()

	// Decoded as JPEG specifically, because that is what everything is stored
	// as regardless of what arrived — one format on disk is one format to serve.
	img, err := jpeg.Decode(file)
	if err != nil {
		t.Fatalf("what was stored is not a JPEG: %v", err)
	}
	return img
}
