-- Additive upgrade for databases created before the steal-queue redesign. On
-- a fresh database TRACK_TIMELINE_GAME.sql already includes this column, so
-- this is a no-op; on an existing database it adds it. The old
-- CHALLENGE_OPENED_ON_DATE column is left in place, unused from here on --
-- renaming it would not be safely idempotent to rerun on every startup the
-- way ADD COLUMN IF NOT EXISTS is.
ALTER TABLE TRACK_TIMELINE_GAME ADD COLUMN IF NOT EXISTS PHASE_STARTED_ON_DATE DATETIME(3) NULL;
