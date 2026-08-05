-- +goose Up

ALTER TABLE settings
  -- What the inn keeps from any refund to cover the card processor's cut
  -- (decision #26). Stripe's fee is charged on the way in and is not returned
  -- when a payment is refunded, so a "full" refund funded from the total would
  -- leave the inn out of pocket by exactly this much on a cancellation it had
  -- no part in.
  --
  -- numeric(6,5) and scaled to hundred-thousandths at the query boundary, the
  -- same way tax_rate is, so no rate is ever decoded into a float64.
  ADD COLUMN refund_processing_rate numeric(6, 5) NOT NULL DEFAULT 0.03
    CHECK (refund_processing_rate >= 0 AND refund_processing_rate < 1),

  -- The longest stay the booking engine sells (decision #27). Anything longer
  -- is a conversation with the owner — rates, cleaning and payment terms for a
  -- month-plus stay are not what the deposit/balance split was designed for.
  ADD COLUMN max_stay_nights integer NOT NULL DEFAULT 31
    CHECK (max_stay_nights >= 1);

-- A maximum below the minimum would make every stay unbookable, and would do it
-- silently: search would simply stop returning rooms. Cross-column, so it has
-- to be a table constraint rather than a column one.
ALTER TABLE settings
  ADD CONSTRAINT settings_stay_range_is_sane
    CHECK (max_stay_nights >= default_min_stay);

COMMENT ON COLUMN settings.refund_processing_rate IS
  'Retained from every refund to cover the card processor. The cancellation penalty absorbs it when that is larger, so a late cancellation is not charged twice.';

COMMENT ON COLUMN settings.max_stay_nights IS
  'Longest bookable stay. The date picker stops offering departures beyond it and the availability query refuses them, so a hand-crafted payload cannot get around it either.';

-- +goose Down
ALTER TABLE settings DROP CONSTRAINT settings_stay_range_is_sane;
ALTER TABLE settings
  DROP COLUMN refund_processing_rate,
  DROP COLUMN max_stay_nights;
