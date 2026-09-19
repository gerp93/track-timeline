-- Additive upgrade for databases created before starting cards were logged.
-- On a fresh database TRACK_TIMELINE_LOG_CARD.sql already has the 'dealt'
-- event and both columns, so this is a no-op; on an existing database it adds
-- them. Every clause is idempotent, so re-running it on each start is safe.
ALTER TABLE TRACK_TIMELINE_LOG_CARD
    ADD COLUMN IF NOT EXISTS USER_ID UUID NULL,
    ADD COLUMN IF NOT EXISTS POOL_SIZE INT NULL,
    MODIFY COLUMN EVENT_TYPE ENUM('drawn', 'discarded', 'skipped', 'bought', 'dealt') NOT NULL;
