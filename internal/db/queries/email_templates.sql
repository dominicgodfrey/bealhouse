-- The owner's edit for one message, if there is one.
--
-- Read on every send rather than cached in the process. A seven-room inn sends
-- a handful of messages a day, so the round trip is free, and the property it
-- buys is that an edit saved in admin applies to the very next message instead
-- of at the next deploy — which is the whole point of the copy being data.
-- name: GetEmailTemplate :one
SELECT name, subject, body, updated_at
FROM email_templates
WHERE name = sqlc.arg(name);

-- Every edit the owner has saved.
--
-- Only the overridden ones: the full list of messages is a property of the
-- binary (internal/email.Names), and the console reads this to find out which
-- of them have been written and which still ship blank.
-- name: ListEmailTemplates :many
SELECT name, subject, body, updated_at
FROM email_templates
ORDER BY name;

-- Save one message's copy.
--
-- The caller must have parsed both halves first. A template that does not
-- compile is refused at send time, and by then a guest's card has been charged
-- and the message that says so is stuck in the queue failing forever.
-- name: UpsertEmailTemplate :exec
INSERT INTO email_templates (name, subject, body)
VALUES (sqlc.arg(name), sqlc.arg(subject), sqlc.arg(body))
ON CONFLICT (name) DO UPDATE
SET subject    = excluded.subject,
    body       = excluded.body,
    updated_at = now();

-- Put a message back to what ships with the binary.
--
-- A delete rather than a rewrite, because the shipped copy lives in the
-- repository: copying it into the row here would give the owner a stale copy of
-- a file that has since moved on.
-- name: DeleteEmailTemplate :execrows
DELETE FROM email_templates WHERE name = sqlc.arg(name);
