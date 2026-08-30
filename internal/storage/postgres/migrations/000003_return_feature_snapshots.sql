CREATE TABLE return_feature_snapshots (
    instrument_id bigint NOT NULL REFERENCES instruments(id),
    as_of timestamptz NOT NULL,
    feature_version text NOT NULL,
    current_price numeric(38, 18),
    current_price_at timestamptz,
    current_source text,
    current_age_seconds integer NOT NULL CHECK (current_age_seconds >= 0),
    recent_quote_volume_1h numeric(38, 18) NOT NULL CHECK (recent_quote_volume_1h >= 0),
    quote_volume_24h numeric(38, 18) NOT NULL CHECK (quote_volume_24h >= 0),
    return_15m numeric(24, 12),
    return_1h numeric(24, 12),
    return_4h numeric(24, 12),
    return_24h numeric(24, 12),
    is_valid_15m boolean NOT NULL,
    is_valid_1h boolean NOT NULL,
    is_valid_4h boolean NOT NULL,
    is_valid_24h boolean NOT NULL,
    quality_json jsonb NOT NULL,
    calculated_at timestamptz NOT NULL,
    PRIMARY KEY (instrument_id, as_of, feature_version),
    CONSTRAINT return_feature_current_price_pair CHECK (
        (current_price IS NULL) = (current_price_at IS NULL)
        AND (current_price IS NULL) = (current_source IS NULL)
    ),
    CONSTRAINT return_feature_current_price_positive CHECK (
        current_price IS NULL OR current_price > 0
    ),
    CONSTRAINT return_feature_valid_15m_value CHECK (is_valid_15m = (return_15m IS NOT NULL)),
    CONSTRAINT return_feature_valid_1h_value CHECK (is_valid_1h = (return_1h IS NOT NULL)),
    CONSTRAINT return_feature_valid_4h_value CHECK (is_valid_4h = (return_4h IS NOT NULL)),
    CONSTRAINT return_feature_valid_24h_value CHECK (is_valid_24h = (return_24h IS NOT NULL))
) PARTITION BY RANGE (as_of);

CREATE TABLE return_feature_snapshots_default
    PARTITION OF return_feature_snapshots DEFAULT;

CREATE INDEX return_feature_snapshots_as_of_version
    ON return_feature_snapshots (as_of DESC, feature_version);

CREATE INDEX return_feature_snapshots_time_brin
    ON return_feature_snapshots USING brin (as_of);
