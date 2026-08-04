-- +goose Up

-- Property-wide configuration the owner edits in admin. Deliberately a single
-- typed row rather than a key/value table: these values do arithmetic on money
-- and dates, so they should be typed and constrained by the database, not
-- parsed out of text at every call site.
CREATE TABLE settings (
  id               boolean PRIMARY KEY DEFAULT true CHECK (id),
  default_min_stay integer NOT NULL DEFAULT 2  CHECK (default_min_stay >= 1),
  tax_rate         numeric(6, 5) NOT NULL DEFAULT 0.085 CHECK (tax_rate >= 0 AND tax_rate < 1),
  hold_ttl_minutes integer NOT NULL DEFAULT 15 CHECK (hold_ttl_minutes > 0),
  checkin_time     time NOT NULL DEFAULT '15:00',
  checkout_time    time NOT NULL DEFAULT '11:00',
  updated_at       timestamptz NOT NULL DEFAULT now()
);

COMMENT ON COLUMN settings.tax_rate IS
  'NH Meals & Rooms tax. Every booking snapshots this value, so changing it here never re-prices a confirmed stay.';

-- The CHECK on the primary key permits exactly one row: true. A second insert
-- collides on the primary key.
INSERT INTO settings DEFAULT VALUES;

-- +goose Down
DROP TABLE settings;
