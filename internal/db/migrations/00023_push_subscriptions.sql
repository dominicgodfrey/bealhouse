-- +goose Up

-- Where a browser wants its notifications delivered.
--
-- One row per browser install that has said yes, not one per phone and not one
-- per session: a push subscription belongs to the browser, survives the console
-- being closed, and is what makes a notification arrive when nobody is looking
-- at the site. That is the whole point of it over anything the page could do.
--
-- The endpoint is the primary key because it already is one — it is a URL the
-- push service minted for this browser and nobody else, and a browser that
-- re-subscribes hands back the same string. That makes the save an upsert with
-- no second identity to keep in step, and it makes a duplicate impossible
-- rather than merely unlikely.
--
-- ON DELETE CASCADE from users, so removing the owner account takes its
-- subscriptions with it. There is nothing to send to afterwards.
CREATE TABLE push_subscriptions (
  endpoint text PRIMARY KEY,

  user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,

  -- The two halves of the browser's encryption keying material. Every message
  -- is encrypted to these before it leaves this server: the push service
  -- carries the payload and cannot read it, which is why a notification may
  -- name a guest at all.
  p256dh text NOT NULL,
  auth   text NOT NULL,

  -- What the owner calls this handset, so the account screen can say which one
  -- is being turned off. Free text and not required to be unique — two phones
  -- called "Mine" is a labelling problem, not a data one.
  label text NOT NULL DEFAULT '',

  created_at   timestamptz NOT NULL DEFAULT now(),
  last_sent_at timestamptz,

  CONSTRAINT push_endpoint_is_a_url CHECK (endpoint LIKE 'https://%'),
  CONSTRAINT push_keys_present CHECK (
    btrim(p256dh, ' ') <> '' AND btrim(auth, ' ') <> ''
  )
);

CREATE INDEX push_subscriptions_user_idx ON push_subscriptions (user_id);

COMMENT ON TABLE push_subscriptions IS
  'Browsers that have agreed to receive notifications for the admin console.';

-- +goose Down
DROP TABLE push_subscriptions;
