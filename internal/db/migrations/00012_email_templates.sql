-- +goose Up

-- The owner's edits to the messages the inn sends.
--
-- Email copy is the owner's the same way room descriptions, photos and the
-- accessibility notice are, and for the same reason: rewording a sentence a
-- guest reads must not need a developer and a deploy. The six — now seven —
-- templates ship blank on purpose, and this is where the words that replace
-- them live.
--
-- **A row per *overridden* template, not per template.** Absent means the
-- shipped file is in force, so a template added in a later release renders from
-- day one without a data migration behind it, and "reset to the original" is a
-- DELETE rather than a copy of text that would then drift from the file it was
-- copied from.
--
-- The layout is deliberately not in here. It carries the letterhead and the
-- table scaffolding that survives Outlook, and an editor that can break every
-- message at once is not a feature the owner asked for.
CREATE TABLE email_templates (
  -- Matches the constants in internal/email — 'booking_confirmation' and so on.
  --
  -- No foreign key and no CHECK against a list: the set of templates is a
  -- property of the binary, and encoding it here would mean a migration every
  -- time a message is added. A row naming a template that does not exist is
  -- inert rather than harmful — nothing ever looks it up.
  name       text PRIMARY KEY,

  subject    text NOT NULL,
  body       text NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),

  -- Blank copy is not an edit, it is a broken message, and a subject line that
  -- is whitespace is what a spam filter looks for. Saving nothing has to be
  -- refused rather than quietly sent; resetting is the DELETE.
  -- The character class is spelled out because btrim's default is spaces
  -- alone, and an editor's empty textarea usually comes back as a newline.
  CONSTRAINT email_template_subject_present CHECK (btrim(subject, E' \t\r\n') <> ''),
  CONSTRAINT email_template_body_present    CHECK (btrim(body, E' \t\r\n') <> '')
);

COMMENT ON TABLE email_templates IS
  'Owner-edited email copy. A row overrides the file shipped in internal/email/templates; no row means the shipped one is used. Deleting a row resets that message to what ships.';

-- +goose Down
DROP TABLE email_templates;
