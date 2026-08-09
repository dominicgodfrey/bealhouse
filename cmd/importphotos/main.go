// Command importphotos is a one-shot: it pulls the inn's photographs off the
// site the owner is running today (thebealhouse.com) and puts them through the
// same pipeline the console's upload button uses.
//
// Throwaway. Once the owner is uploading through the console this has no reason
// to exist — delete it. It is checked in only so the provisional content in
// internal/db/seed/ can be reproduced from scratch.
//
//	go run ./cmd/importphotos
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bealhouse/internal/media"
)

// A photograph on the owner's current site: where to fetch it, and the alt text
// it should carry here.
//
// Alt text is REQUIRED by the schema (NOT NULL plus a CHECK), and the WordPress
// media library has it on only a handful. Where the owner wrote one it is theirs,
// copied exactly. Where they did not, the text below describes what is visibly
// in the frame and nothing more — no adjectives about how the room feels, which
// would be inventing copy. These are the lines most worth the owner's review.
type shot struct {
	url string
	alt string
}

// Keyed by room slug. The mapping came from the WordPress REST API, which
// attaches each image to its room post, so this is the owner's own grouping
// rather than a guess from filenames.
var rooms = map[string][]shot{
	"mrs-beals-suite": {
		{"2026/01/20260110_122913-scaled.jpg", "Mrs. Beal's Suite bedroom, with a queen bed"},
		{"2026/01/20260110_123044-scaled.jpg", "Mrs. Beal's Suite sitting room"},
		{"2026/01/20260110_123406-scaled.jpg", "The door to Mrs. Beal's Suite"},
		{"2026/01/20260110_122952-scaled.jpg", "Photo of Mrs. Beal's Suite Bathroom"},
	},
	"garden-suite": {
		{"2026/01/20260110_124737-scaled.jpg", "Photo of Garden Suite front bedroom"},
		{"2026/01/20260110_124530-scaled.jpg", "Photo of Garden suite back bedroom"},
		{"2026/01/20260110_124545-scaled.jpg", "Alternate photo of Garden Suite back bedroom"},
		{"2026/01/20260110_124626-scaled.jpg", "Garden Suite Bathroom"},
		{"2026/01/20260110_125647-scaled.jpg", "Photo of Garden Suite entrance"},
	},
	// 20260110_124114 is NOT here, although WordPress attaches it to this room.
	// It is byte-identical to the file attached to the Blue Room, whose own alt
	// text on the owner's site reads "Bedroom in Blue Room" — so the attachment
	// here is theirs to correct, and until they do, showing one bedroom as two
	// different rooms is the version a guest could be misled by.
	"rose-chamber": {
		{"2026/01/20260110_1232531-scaled.jpg", "Queen Bed full bath on 1st floor"},
		{"2026/01/20260110_123527-scaled.jpg", "Rose Chamber bathroom, with a shower over the bath"},
		{"2026/01/20260110_123725-scaled.jpg", "The door to Rose Chamber"},
	},
	// 20260110_123954-1 is the same photograph as 20260110_123954, uploaded
	// twice. Content addressing means both rows would point at one file and the
	// page would show the bathroom twice in a row.
	"blue-room": {
		{"2026/01/20260110_124114-2-scaled.jpg", "Bedroom in Blue Room"},
		{"2026/01/20260110_123954-scaled.jpg", "Blue Room bathroom"},
		{"2026/01/20260110_125735-scaled.jpg", "The door to the Blue Room"},
	},
	"washington-room": {
		{"2026/01/20260110_125408-scaled.jpg", "Photo of Washington Room bedroom"},
		{"2026/01/20260110_125607-scaled.jpg", "Photo of Washington Room entrance"},
		{"2026/01/20260110_125234-scaled.jpg", "Photo of Washington Room Bathroom"},
	},
}

// Keyed by page slug — the same slugs as console.PageSlugs(). These are the
// images the owner has on the corresponding section of their current site.
var pages = map[string][]shot{
	// Described by what is visibly on the plate and no further. Naming the
	// recipes would be writing the menu, which is the owner's and lives in the
	// console.
	"restaurant": {
		{"2026/01/IMG_20260106_205133.jpg", "A plate of glazed pork with scallions, served with rice"},
		{"2026/01/IMG_20260106_203401.jpg", "A bowl of seafood pasta with shrimp and herbs"},
		{"2026/01/IMG_20260106_205505.png", "Fresh summer rolls with shrimp, with a dipping sauce"},
		{"2026/01/IMG_20260106_204747.jpg", "A plate of sliced cucumber in a chilli dressing"},
	},
	"events": {
		{"2025/12/1000021251.jpg", "A group gathered outside the Beal House, arms raised"},
	},
	// The town, not the inn. winter_beal.jpg is a photograph of the Beal House
	// itself and belongs on the home page below — on a page about what is around
	// the inn, a picture of the inn is the one thing that is not the subject.
	"local-area": {
		{"2026/03/downtown_littleton.jpg", "Main Street in downtown Littleton, New Hampshire"},
		{"2026/03/summer_river.jpg", "Littleton's River District from above, beside the river in summer"},
	},
	"home": {
		{"2026/03/winter_beal.jpg", "The Beal House under snow in winter"},
	},
}

const base = "https://thebealhouse.com/wp-content/uploads/"

func main() {
	ctx := context.Background()

	dir := os.Getenv("MEDIA_DIR")
	if dir == "" {
		dir = "media"
	}
	store, err := media.New(dir)
	if err != nil {
		log.Fatal(err)
	}

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://bealhouse:bealhouse@localhost:5432/bealhouse?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	client := &http.Client{Timeout: 60 * time.Second}

	// Rooms.
	for slug, shots := range rooms {
		if _, err := pool.Exec(ctx,
			`DELETE FROM room_photos WHERE room_id = (SELECT id FROM rooms WHERE slug = $1)`,
			slug); err != nil {
			log.Fatalf("clearing %s: %v", slug, err)
		}
		for i, s := range shots {
			path := fetchAndStore(client, store, s.url)
			if _, err := pool.Exec(ctx,
				`INSERT INTO room_photos (room_id, path, alt_text, sort_order)
				 SELECT id, $2, $3, $4 FROM rooms WHERE slug = $1`,
				slug, path, s.alt, i); err != nil {
				log.Fatalf("inserting %s[%d]: %v", slug, i, err)
			}
		}
		fmt.Printf("%-16s %d photos\n", slug, len(shots))
	}

	// Pages.
	for slug, shots := range pages {
		if _, err := pool.Exec(ctx, `DELETE FROM page_photos WHERE slug = $1`, slug); err != nil {
			log.Fatalf("clearing %s: %v", slug, err)
		}
		for i, s := range shots {
			path := fetchAndStore(client, store, s.url)
			if _, err := pool.Exec(ctx,
				`INSERT INTO page_photos (slug, path, alt_text, sort_order) VALUES ($1, $2, $3, $4)`,
				slug, path, s.alt, i); err != nil {
				log.Fatalf("inserting %s[%d]: %v", slug, i, err)
			}
		}
		fmt.Printf("%-16s %d photos\n", slug, len(shots))
	}
}

// fetchAndStore downloads one image and puts it through media.Store.Save, which
// is the same call the console's upload handler makes: decoded (the only real
// check that it is an image), scaled to 2400px, and written under the SHA-256 of
// its own bytes along with every rung of the ladder in JPEG and WebP.
func fetchAndStore(client *http.Client, store *media.Store, name string) string {
	// The host rate-limits a burst, answering 403 or 406 to requests that
	// succeed perfectly well on their own. This is somebody's live site, so the
	// answer is to go slowly and back off rather than to hammer it.
	for attempt := range 10 {
		if attempt > 0 {
			time.Sleep(time.Duration(2*attempt*attempt) * time.Second)
		}

		req, err := http.NewRequest(http.MethodGet, base+name, nil)
		if err != nil {
			log.Fatalf("fetching %s: %v", name, err)
		}
		req.Header.Set("User-Agent", "bealhouse-importphotos/1.0")

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("fetching %s: %v — retrying", name, err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			log.Printf("fetching %s: %s — retrying", name, resp.Status)
			continue
		}

		path, err := store.Save(resp.Body)
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Fatalf("storing %s: %v", name, err)
		}

		time.Sleep(time.Second)
		return path
	}

	log.Fatalf("fetching %s: gave up after ten attempts", name)
	return ""
}
