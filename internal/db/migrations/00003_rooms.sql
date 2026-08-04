-- +goose Up

CREATE TABLE rooms (
  id            bigserial PRIMARY KEY,
  slug          text NOT NULL UNIQUE,
  name          text NOT NULL,
  description   text NOT NULL DEFAULT '',
  view          text,
  max_occupancy integer NOT NULL CHECK (max_occupancy > 0),
  amenities     text[] NOT NULL DEFAULT '{}',

  is_accessible          boolean NOT NULL DEFAULT false,
  accessibility_features text[] NOT NULL DEFAULT '{}',

  is_pet_friendly boolean NOT NULL DEFAULT false,
  pet_fee_cents   integer NOT NULL DEFAULT 0 CHECK (pet_fee_cents >= 0),

  sort_order integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  -- The honesty rule from ARCHITECTURE.md, enforced by the database rather than
  -- by the admin form. "Accessible" is a promise a guest plans a trip around; a
  -- wheelchair user who arrives to find three steps has been genuinely harmed.
  -- Any code path that sets the flag must also say what is actually true.
  CONSTRAINT accessible_rooms_must_list_features
    CHECK (NOT is_accessible OR cardinality(accessibility_features) > 0),

  -- A fee on a room that does not accept pets is unchargeable, so it is a bug.
  CONSTRAINT pet_fee_requires_pet_friendly
    CHECK (is_pet_friendly OR pet_fee_cents = 0)
);

CREATE INDEX rooms_sort_order_idx ON rooms (sort_order);

CREATE TABLE room_beds (
  id       bigserial PRIMARY KEY,
  room_id  bigint NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
  bed_type text NOT NULL CHECK (bed_type IN ('king', 'queen', 'full', 'twin', 'daybed', 'sofa_bed')),
  count    integer NOT NULL DEFAULT 1 CHECK (count > 0),
  -- Which part of the room, e.g. 'sitting room'. Empty rather than NULL so the
  -- uniqueness constraint below actually fires.
  location text NOT NULL DEFAULT '',

  UNIQUE (room_id, bed_type, location)
);

CREATE INDEX room_beds_room_id_idx ON room_beds (room_id);

-- +goose Down
DROP TABLE room_beds;
DROP TABLE rooms;
