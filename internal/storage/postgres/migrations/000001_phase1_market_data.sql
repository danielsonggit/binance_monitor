CREATE TABLE instruments (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    symbol text NOT NULL,
    base_asset text NOT NULL,
    quote_asset text NOT NULL,
    sector text NOT NULL CHECK (sector IN ('CRYPTO', 'TRADFI')),
    contract_type text NOT NULL,
    status text NOT NULL,
    price_precision integer,
    quantity_precision integer,
    valid_from timestamptz NOT NULL,
    valid_to timestamptz,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT instruments_valid_range CHECK (valid_to IS NULL OR valid_to > valid_from),
    CONSTRAINT instruments_symbol_valid_from_unique UNIQUE (symbol, valid_from)
);

CREATE UNIQUE INDEX instruments_one_active_symbol
    ON instruments (symbol)
    WHERE valid_to IS NULL;

CREATE INDEX instruments_active_sector_symbol
    ON instruments (sector, symbol)
    WHERE valid_to IS NULL;

CREATE TABLE market_snapshots_5m (
    instrument_id bigint NOT NULL REFERENCES instruments(id),
    bucket_time timestamptz NOT NULL,
    last_price numeric(38, 18) NOT NULL CHECK (last_price > 0),
    price_change_percent_24h numeric(20, 10),
    base_volume_24h numeric(38, 18),
    quote_volume_24h numeric(38, 18),
    source_event_time timestamptz,
    received_at timestamptz NOT NULL,
    quality_score smallint NOT NULL CHECK (quality_score BETWEEN 0 AND 100),
    PRIMARY KEY (instrument_id, bucket_time)
) PARTITION BY RANGE (bucket_time);

CREATE TABLE market_snapshots_5m_default
    PARTITION OF market_snapshots_5m DEFAULT;

CREATE INDEX market_snapshots_5m_time_brin
    ON market_snapshots_5m USING brin (bucket_time);

CREATE TABLE klines_15m (
    instrument_id bigint NOT NULL REFERENCES instruments(id),
    open_time timestamptz NOT NULL,
    close_time timestamptz NOT NULL,
    open numeric(38, 18) NOT NULL CHECK (open > 0),
    high numeric(38, 18) NOT NULL CHECK (high > 0),
    low numeric(38, 18) NOT NULL CHECK (low > 0),
    close numeric(38, 18) NOT NULL CHECK (close > 0),
    volume numeric(38, 18) NOT NULL CHECK (volume >= 0),
    quote_volume numeric(38, 18) NOT NULL CHECK (quote_volume >= 0),
    trade_count bigint NOT NULL CHECK (trade_count >= 0),
    taker_buy_base_volume numeric(38, 18) NOT NULL CHECK (taker_buy_base_volume >= 0),
    taker_buy_quote_volume numeric(38, 18) NOT NULL CHECK (taker_buy_quote_volume >= 0),
    source text NOT NULL,
    received_at timestamptz NOT NULL,
    PRIMARY KEY (instrument_id, open_time),
    CONSTRAINT klines_15m_time_range CHECK (close_time > open_time),
    CONSTRAINT klines_15m_ohlc CHECK (high >= low AND high >= open AND high >= close AND low <= open AND low <= close)
) PARTITION BY RANGE (open_time);

CREATE TABLE klines_15m_default
    PARTITION OF klines_15m DEFAULT;

CREATE INDEX klines_15m_time_brin
    ON klines_15m USING brin (open_time);

CREATE TABLE collection_runs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    idempotency_key text NOT NULL UNIQUE,
    job_type text NOT NULL,
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    expected_count integer NOT NULL DEFAULT 0 CHECK (expected_count >= 0),
    actual_count integer NOT NULL DEFAULT 0 CHECK (actual_count >= 0),
    missing_count integer NOT NULL DEFAULT 0 CHECK (missing_count >= 0),
    status text NOT NULL CHECK (status IN ('RUNNING', 'SUCCEEDED', 'DEGRADED', 'FAILED')),
    error_message text,
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT collection_runs_window CHECK (window_end > window_start),
    CONSTRAINT collection_runs_counts CHECK (actual_count + missing_count <= expected_count OR expected_count = 0)
);

CREATE INDEX collection_runs_job_window
    ON collection_runs (job_type, window_start DESC);

CREATE TABLE system_heartbeats (
    component text PRIMARY KEY,
    observed_at timestamptz NOT NULL,
    status text NOT NULL CHECK (status IN ('STARTING', 'HEALTHY', 'DEGRADED', 'UNHEALTHY', 'STOPPING')),
    detail_json jsonb NOT NULL DEFAULT '{}'::jsonb
);
