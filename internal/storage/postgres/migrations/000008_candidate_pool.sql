CREATE TABLE candidate_rule_versions (
    rule_version text PRIMARY KEY,
    feature_version text NOT NULL,
    direction text NOT NULL CHECK (direction IN ('LONG')),
    config_json jsonb NOT NULL,
    checksum_sha256 text NOT NULL CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL,
    CONSTRAINT candidate_rule_version_checksum_unique UNIQUE (checksum_sha256)
);

CREATE TABLE candidate_pool_members (
    instrument_id bigint NOT NULL REFERENCES instruments(id),
    direction text NOT NULL CHECK (direction IN ('LONG')),
    rule_version text NOT NULL REFERENCES candidate_rule_versions(rule_version),
    status text NOT NULL CHECK (status IN ('ACTIVE', 'COOLDOWN')),
    entered_at timestamptz NOT NULL,
    last_selected_at timestamptz NOT NULL,
    last_evaluated_at timestamptz NOT NULL,
    consecutive_misses integer NOT NULL CHECK (consecutive_misses >= 0),
    cooldown_until timestamptz,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (instrument_id, direction, rule_version),
    CONSTRAINT candidate_pool_member_times CHECK (
        last_selected_at >= entered_at
        AND last_evaluated_at >= entered_at
        AND updated_at >= last_evaluated_at
    ),
    CONSTRAINT candidate_pool_member_cooldown CHECK (
        (status = 'ACTIVE' AND cooldown_until IS NULL)
        OR (status = 'COOLDOWN' AND cooldown_until IS NOT NULL)
    )
);

CREATE INDEX candidate_pool_members_active
    ON candidate_pool_members (rule_version, direction, status, last_selected_at DESC);

CREATE TABLE candidate_evaluations (
    instrument_id bigint NOT NULL REFERENCES instruments(id),
    as_of timestamptz NOT NULL,
    rule_version text NOT NULL REFERENCES candidate_rule_versions(rule_version),
    feature_version text NOT NULL,
    sector text NOT NULL CHECK (sector IN ('CRYPTO', 'TRADFI')),
    direction text NOT NULL CHECK (direction IN ('LONG')),
    availability_state text NOT NULL CHECK (availability_state IN (
        'OPEN', 'MARKET_CLOSED', 'LOW_ACTIVITY', 'DATA_MISSING', 'SOURCE_UNAVAILABLE', 'UNKNOWN'
    )),
    return_15m numeric(24, 12),
    return_1h numeric(24, 12),
    is_valid_15m boolean NOT NULL,
    is_valid_1h boolean NOT NULL,
    percentile_15m numeric(9, 6) NOT NULL CHECK (percentile_15m BETWEEN 0 AND 100),
    percentile_1h numeric(9, 6) NOT NULL CHECK (percentile_1h BETWEEN 0 AND 100),
    threshold_15m numeric(24, 12) NOT NULL,
    threshold_1h numeric(24, 12) NOT NULL,
    recent_quote_volume_1h numeric(38, 18) NOT NULL CHECK (recent_quote_volume_1h >= 0),
    quote_volume_24h numeric(38, 18) NOT NULL CHECK (quote_volume_24h >= 0),
    trigger_15m boolean NOT NULL,
    trigger_1h boolean NOT NULL,
    liquidity_qualified boolean NOT NULL,
    priority_ratio numeric(24, 12) NOT NULL CHECK (priority_ratio >= 0),
    capacity_rank integer NOT NULL CHECK (capacity_rank >= 0),
    prior_status text CHECK (prior_status IS NULL OR prior_status IN ('ACTIVE', 'COOLDOWN')),
    outcome text NOT NULL CHECK (outcome IN (
        'ENTERED', 'CONTINUED', 'MISS_HELD', 'EXITED',
        'REJECTED_QUALITY', 'REJECTED_MOMENTUM', 'REJECTED_LIQUIDITY',
        'REJECTED_CAPACITY', 'REJECTED_COOLDOWN'
    )),
    consecutive_misses integer NOT NULL CHECK (consecutive_misses >= 0),
    cooldown_until timestamptz,
    reasons_json jsonb NOT NULL,
    calculated_at timestamptz NOT NULL,
    PRIMARY KEY (instrument_id, as_of, rule_version),
    CONSTRAINT candidate_evaluation_valid_15m CHECK (is_valid_15m = (return_15m IS NOT NULL)),
    CONSTRAINT candidate_evaluation_valid_1h CHECK (is_valid_1h = (return_1h IS NOT NULL))
) PARTITION BY RANGE (as_of);

CREATE TABLE candidate_evaluations_default
    PARTITION OF candidate_evaluations DEFAULT;

CREATE INDEX candidate_evaluations_latest_outcome
    ON candidate_evaluations (rule_version, sector, outcome, as_of DESC);

CREATE INDEX candidate_evaluations_instrument_time
    ON candidate_evaluations (instrument_id, rule_version, as_of DESC);

CREATE INDEX candidate_evaluations_time_brin
    ON candidate_evaluations USING brin (as_of);
