ALTER TABLE ranking_snapshots
    ADD COLUMN positive_count integer NOT NULL DEFAULT 0;

UPDATE ranking_snapshots
SET positive_count = ranked_count;

ALTER TABLE ranking_snapshots
    ADD CONSTRAINT ranking_snapshots_positive_count CHECK (
        positive_count >= 0
        AND positive_count <= eligible_count
        AND ranked_count <= positive_count
    );

ALTER TABLE ranking_snapshots
    ALTER COLUMN positive_count DROP DEFAULT;

