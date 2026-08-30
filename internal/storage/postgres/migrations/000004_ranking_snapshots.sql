CREATE TABLE ranking_snapshots (
    id bigint GENERATED ALWAYS AS IDENTITY,
    as_of timestamptz NOT NULL,
    ranking_version text NOT NULL,
    feature_version text NOT NULL,
    sector text NOT NULL CHECK (sector IN ('CRYPTO', 'TRADFI')),
    horizon text NOT NULL CHECK (horizon IN ('15m', '1h', '4h', '24h')),
    requested_limit integer NOT NULL CHECK (requested_limit > 0),
    active_count integer NOT NULL CHECK (active_count >= 0),
    eligible_count integer NOT NULL CHECK (eligible_count >= 0),
    ranked_count integer NOT NULL CHECK (ranked_count >= 0),
    calculated_at timestamptz NOT NULL,
    PRIMARY KEY (id, as_of),
    CONSTRAINT ranking_snapshots_identity UNIQUE (as_of, ranking_version, feature_version, sector, horizon),
    CONSTRAINT ranking_snapshots_counts CHECK (
        eligible_count <= active_count
        AND ranked_count <= eligible_count
        AND ranked_count <= requested_limit
    )
) PARTITION BY RANGE (as_of);

CREATE TABLE ranking_snapshots_default
    PARTITION OF ranking_snapshots DEFAULT;

CREATE INDEX ranking_snapshots_latest
    ON ranking_snapshots (sector, horizon, as_of DESC, ranking_version, feature_version);

CREATE TABLE ranking_snapshot_items (
    ranking_snapshot_id bigint NOT NULL,
    as_of timestamptz NOT NULL,
    rank_position integer NOT NULL CHECK (rank_position > 0),
    instrument_id bigint NOT NULL REFERENCES instruments(id),
    return_percent numeric(24, 12) NOT NULL,
    current_price numeric(38, 18) NOT NULL CHECK (current_price > 0),
    quote_volume_24h numeric(38, 18) NOT NULL CHECK (quote_volume_24h >= 0),
    percentile numeric(9, 6) NOT NULL CHECK (percentile >= 0 AND percentile <= 100),
    PRIMARY KEY (ranking_snapshot_id, as_of, rank_position),
    CONSTRAINT ranking_snapshot_items_symbol_unique UNIQUE (ranking_snapshot_id, as_of, instrument_id),
    CONSTRAINT ranking_snapshot_items_snapshot_fk
        FOREIGN KEY (ranking_snapshot_id, as_of)
        REFERENCES ranking_snapshots(id, as_of)
        ON DELETE CASCADE
) PARTITION BY RANGE (as_of);

CREATE TABLE ranking_snapshot_items_default
    PARTITION OF ranking_snapshot_items DEFAULT;

CREATE INDEX ranking_snapshot_items_instrument_time
    ON ranking_snapshot_items (instrument_id, as_of DESC);
