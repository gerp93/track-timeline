-- Marks a card whose YouTube link was just edited and has not been re-checked
-- against the API yet. Cleared by SetVideoStatus on the next successful check.
-- On a fresh database TRACK_TIMELINE_CARD_VIDEO_STATUS.sql already includes
-- this column; this is the additive upgrade for older DBs.
ALTER TABLE TRACK_TIMELINE_CARD_VIDEO_STATUS
    ADD COLUMN IF NOT EXISTS AWAITING_VALIDATION TINYINT(1) NOT NULL DEFAULT 0;
