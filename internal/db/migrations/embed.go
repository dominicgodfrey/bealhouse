// Package migrations carries the schema's own history, embedded.
//
// The .sql files beside this one are the source of truth and are still what
// `go tool goose -dir internal/db/migrations` reads on a developer's machine.
// Embedding them as well is what lets `bealhouse migrate` bring a database up
// to the shape the binary expects with nothing else installed on the server.
//
// That is worth more than saving a step on a deploy. A binary and the schema it
// was built against travel as one artifact, so there is no version of the
// deploy where the code on the box and the migrations applied to the database
// came from different commits — which is otherwise the failure that presents as
// a column that does not exist, an hour after everyone agreed the deploy went
// fine.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
