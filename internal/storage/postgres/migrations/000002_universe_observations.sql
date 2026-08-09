ALTER TABLE instruments
    ADD COLUMN last_seen_at timestamptz,
    ADD COLUMN missing_observations smallint NOT NULL DEFAULT 0
        CHECK (missing_observations >= 0);

UPDATE instruments
SET last_seen_at = valid_from
WHERE last_seen_at IS NULL;

ALTER TABLE instruments
    ALTER COLUMN last_seen_at SET NOT NULL;

CREATE INDEX instruments_active_last_seen
    ON instruments (last_seen_at)
    WHERE valid_to IS NULL;
