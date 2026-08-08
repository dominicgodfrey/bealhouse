package media

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"sort"
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

	// The whole ladder deduplicates, not just the canonical file — and nothing
	// is left lying about, because the temporary file each write goes through is
	// removed whether or not the rename happened.
	after := stored(t, store)
	if len(after) != 6 {
		t.Errorf("%d files for a 1200px image (%v), want 6: 480/960/1200 in jpg and webp",
			len(after), after)
	}
	for _, name := range after {
		if strings.HasPrefix(name, ".upload-") {
			t.Errorf("a temporary file was left behind: %s", name)
		}
	}
}

// The ladder, and its ceiling.
//
// The widths are what this change is actually for: a phone rendering a card
// four hundred pixels wide was downloading the 2400px file. What must not
// happen is a rung *above* the source, which would be a larger file that looks
// worse than the original.
func TestTheLadderStopsAtTheSourcesOwnWidth(t *testing.T) {
	for _, c := range []struct {
		name          string
		width, height int
		want          []int
	}{
		{"larger than the top rung", 3600, 2400, []int{480, 960, 1600, 2400}},
		{"between rungs", 1200, 900, []int{480, 960, 1200}},
		{"below every rung", 400, 300, []int{400}},
	} {
		t.Run(c.name, func(t *testing.T) {
			store := newStore(t)
			path, err := store.Save(bytes.NewReader(pngOf(t, c.width, c.height)))
			if err != nil {
				t.Fatalf("saving: %v", err)
			}

			_, canonical, ok := split(Name(path))
			if !ok {
				t.Fatalf("Save returned %q, which carries no width", path)
			}
			if got := widths(canonical); !equal(got, c.want) {
				t.Errorf("ladder is %v, want %v", got, c.want)
			}

			// Every rung is really on disk, at really that width.
			for _, w := range c.want {
				img := decodeFile(t, store, strings.TrimPrefix(path, URLPrefix), w)
				if img.Bounds().Dx() != w {
					t.Errorf("the %dw rung is %dpx wide", w, img.Bounds().Dx())
				}
			}
		})
	}
}

// The property that matters most here, and the reason the width is in the
// filename rather than in a column or a directory listing.
//
// A 404 inside a srcset does not degrade to the fallback — the browser has
// already committed to that candidate, and the result is a broken image on the
// page with nothing anywhere to say why. So every URL Sources hands out has to
// be a file that Save actually wrote.
func TestEveryURLInASrcsetIsAFileThatExists(t *testing.T) {
	store := newStore(t)

	for _, size := range [][2]int{{3600, 2400}, {1200, 900}, {700, 1400}, {400, 300}} {
		path, err := store.Save(bytes.NewReader(pngOf(t, size[0], size[1])))
		if err != nil {
			t.Fatalf("saving: %v", err)
		}

		ladder := Sources(path)
		urls := append(entries(ladder.JPEG), entries(ladder.WebP)...)
		if len(urls) == 0 {
			t.Errorf("%dx%d produced no sources at all", size[0], size[1])
		}
		for _, url := range urls {
			name := Name(url)
			if name == "" {
				t.Errorf("srcset carries %q, which Name refuses", url)
				continue
			}
			if _, err := os.Stat(filepath.Join(store.Dir(), name)); err != nil {
				t.Errorf("srcset advertises %q, which is not on disk", url)
			}
		}
	}
}

// A path stored before the ladder existed still works: the file is real, it
// simply has no other sizes. An empty srcset renders the plain <img src>, which
// is why the fallback has to be a whole file and not a rung.
func TestAPathFromBeforeTheLadderOffersNoSources(t *testing.T) {
	for _, path := range []string{
		"/media/abc123.jpg",   // the old naming
		"/media/photo.jpg",    // never ours
		"/placeholders/x.svg", // not media at all
		"",
	} {
		if got := (Sources(path)); got.JPEG != "" || got.WebP != "" {
			t.Errorf("Sources(%q) = %+v, want nothing", path, got)
		}
	}
}

// WebP is only worth its dependency if it is actually smaller. 80 against the
// JPEG's 85 is chosen to be visually equal and about a third less.
func TestTheWebPRungIsSmallerThanTheJPEG(t *testing.T) {
	store := newStore(t)

	path, err := store.Save(bytes.NewReader(pngOf(t, 1600, 1200)))
	if err != nil {
		t.Fatalf("saving: %v", err)
	}
	stem, _, _ := split(Name(path))

	jpg := size(t, store, rung(stem, 960, ".jpg"))
	wp := size(t, store, rung(stem, 960, ".webp"))
	if wp >= jpg {
		t.Errorf("webp is %d bytes against the jpeg's %d; the format is buying nothing", wp, jpg)
	}
}

// Sources is pure — no directory, no Store — which is what lets the handlers
// that assemble room cards and search results call it without one being
// threaded through three packages. If it ever starts reading the disk, that
// stops being true quietly.
func TestSourcesRoundTripsTheNamingRule(t *testing.T) {
	name := rung("deadbeef", 960, ".jpg")
	stem, width, ok := split(name)
	if !ok || stem != "deadbeef" || width != 960 {
		t.Fatalf("split(%q) = %q, %d, %v", name, stem, width, ok)
	}

	ladder := Sources(URLPrefix + rung("deadbeef", 2400, ".jpg"))
	for _, want := range []string{
		"/media/deadbeef-w480.jpg 480w",
		"/media/deadbeef-w2400.jpg 2400w",
	} {
		if !strings.Contains(ladder.JPEG, want) {
			t.Errorf("srcset %q is missing %q", ladder.JPEG, want)
		}
	}
	if !strings.Contains(ladder.WebP, "/media/deadbeef-w960.webp 960w") {
		t.Errorf("webp srcset %q is missing the 960 rung", ladder.WebP)
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

// stored lists what is in the directory, sorted, for assertions that care about
// the whole set rather than one file.
func stored(t *testing.T, store *Store) []string {
	t.Helper()

	entries, err := os.ReadDir(store.Dir())
	if err != nil {
		t.Fatalf("reading the store: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// decodeFile opens one rung of the ladder that canonical belongs to.
func decodeFile(t *testing.T, store *Store, canonical string, width int) image.Image {
	t.Helper()

	stem, _, ok := split(canonical)
	if !ok {
		t.Fatalf("%q carries no width", canonical)
	}

	file, err := os.Open(filepath.Join(store.Dir(), rung(stem, width, ".jpg")))
	if err != nil {
		t.Fatalf("opening the %dw rung: %v", width, err)
	}
	defer file.Close()

	img, err := jpeg.Decode(file)
	if err != nil {
		t.Fatalf("the %dw rung is not a JPEG: %v", width, err)
	}
	return img
}

func size(t *testing.T, store *Store, name string) int64 {
	t.Helper()
	info, err := os.Stat(filepath.Join(store.Dir(), name))
	if err != nil {
		t.Fatalf("stat %s: %v", name, err)
	}
	return info.Size()
}

// entries pulls the URLs out of a srcset attribute value.
func entries(srcset string) []string {
	if srcset == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(srcset, ", ") {
		out = append(out, strings.Fields(part)[0])
	}
	return out
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
