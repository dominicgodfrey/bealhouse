// Command importowners is a one-shot, like importphotos beside it: it puts the
// photograph of Hwasoo and Tom through media.Store and attaches it to the About
// page.
//
// Throwaway. Once the owner uploads through the console that row is theirs and
// this can go. It is checked in — with its source image, which importphotos
// does not need because it fetches from the live site — so that a fresh clone
// can reproduce the About page's photograph. MEDIA_DIR is not in git and not in
// pg_dump, so without this the row would point at a file nobody else has.
//
//	go run ./cmd/importowners
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"bealhouse/internal/media"
)

const alt = "Hwasoo and Tom, laughing, on a bench outside a bookshop"

// source is the photograph beside this file. Overridable by argument, because
// the next one the owner sends will not be called this.
const source = "cmd/importowners/owners.jpg"

func main() {
	file := source
	if len(os.Args) == 2 {
		file = os.Args[1]
	}

	dir := os.Getenv("MEDIA_DIR")
	if dir == "" {
		dir = "media"
	}
	store, err := media.New(dir)
	if err != nil {
		log.Fatal(err)
	}

	f, err := os.Open(file)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	path, err := store.Save(f)
	if err != nil {
		log.Fatal(err)
	}

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://bealhouse:bealhouse@localhost:5432/bealhouse?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `DELETE FROM page_photos WHERE slug = 'about'`); err != nil {
		log.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO page_photos (slug, path, alt_text, sort_order) VALUES ('about', $1, $2, 0)`,
		path, alt); err != nil {
		log.Fatal(err)
	}

	fmt.Println("about:", path)
}
