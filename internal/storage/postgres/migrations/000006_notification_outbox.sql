CREATE TABLE notification_outbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    idempotency_key text NOT NULL UNIQUE,
    notification_type text NOT NULL CHECK (notification_type IN ('SCHEDULED_MARKET_REPORT')),
    scheduled_for timestamptz NOT NULL,
    data_as_of timestamptz NOT NULL,
    payload_json jsonb NOT NULL,
    status text NOT NULL CHECK (status IN ('PENDING', 'PROCESSING', 'RETRY', 'SENT', 'DEAD', 'UNKNOWN')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL CHECK (max_attempts > 0 AND max_attempts <= 10),
    next_attempt_at timestamptz NOT NULL,
    locked_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz,
    CONSTRAINT notification_outbox_time_order CHECK (data_as_of >= scheduled_for),
    CONSTRAINT notification_outbox_sent_time CHECK ((status = 'SENT') = (sent_at IS NOT NULL))
);

CREATE INDEX notification_outbox_due
    ON notification_outbox (next_attempt_at, id)
    WHERE status IN ('PENDING', 'RETRY');

CREATE TABLE notification_deliveries (
    outbox_id bigint NOT NULL REFERENCES notification_outbox(id) ON DELETE CASCADE,
    chat_id text NOT NULL,
    part_index integer NOT NULL CHECK (part_index >= 0),
    status text NOT NULL CHECK (status IN ('PENDING', 'SENDING', 'SENT', 'FAILED', 'UNKNOWN')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    telegram_message_id bigint,
    last_error text,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (outbox_id, chat_id, part_index),
    CONSTRAINT notification_deliveries_message_id CHECK (
        (status = 'SENT') = (telegram_message_id IS NOT NULL)
    )
);

CREATE INDEX notification_deliveries_outbox_status
    ON notification_deliveries (outbox_id, status, chat_id, part_index);

