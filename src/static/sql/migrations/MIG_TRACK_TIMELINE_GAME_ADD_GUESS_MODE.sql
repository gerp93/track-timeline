-- Per-lobby free-form title/artist guess mode. On a fresh database
-- TRACK_TIMELINE_GAME.sql already includes GUESS_MODE, so this is a no-op;
-- on an existing database it adds the column. ADD COLUMN IF NOT EXISTS is
-- idempotent.
ALTER TABLE TRACK_TIMELINE_GAME
    ADD COLUMN IF NOT EXISTS GUESS_MODE ENUM('off', 'both', 'title', 'either') NOT NULL DEFAULT 'both';
