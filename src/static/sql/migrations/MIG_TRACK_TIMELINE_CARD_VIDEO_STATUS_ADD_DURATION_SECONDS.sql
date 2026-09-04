-- Additive upgrade for databases created before video duration was recorded.
-- On a fresh database TRACK_TIMELINE_CARD_VIDEO_STATUS.sql already includes
-- this column, so this is a no-op; on an existing database it adds it. ADD
-- COLUMN IF NOT EXISTS is idempotent -- safe to always rerun. Existing rows
-- get NULL (unknown) and pick up a real length on the next video check.
ALTER TABLE TRACK_TIMELINE_CARD_VIDEO_STATUS ADD COLUMN IF NOT EXISTS DURATION_SECONDS INT NULL;
