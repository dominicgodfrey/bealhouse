-- +goose Up

-- Say "inquiry", not "enquiry".
--
-- One spelling across the console, the public pages and the schema, so the
-- word the owner reads on the tab is the word the code uses. The British
-- spelling was only ever in prose — the table has been `event_inquiries` and
-- the routes `/admin/inquiries` since they were written.
--
-- A migration rather than an edit to 00020, because that one is applied: the
-- comment string in the database is set by whichever statement ran, and a file
-- nobody re-runs is not the same as a column nobody re-comments. Re-issuing it
-- here is also what keeps `sqlc` output and the live schema saying the same
-- thing, since sqlc reads these files in order and takes the last word.
COMMENT ON COLUMN event_inquiries.kind IS
  'Which form wrote the row: the events inquiry form, or the general contact form on the home page.';

-- +goose Down
COMMENT ON COLUMN event_inquiries.kind IS
  'Which form wrote the row: the events enquiry form, or the general contact form on the home page.';
