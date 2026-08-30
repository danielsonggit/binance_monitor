ALTER TABLE instruments
    ADD COLUMN exchange_status text NOT NULL DEFAULT 'TRADING';

CREATE INDEX instruments_current_exchange_status
    ON instruments (exchange_status, sector, symbol)
    WHERE valid_to IS NULL;
