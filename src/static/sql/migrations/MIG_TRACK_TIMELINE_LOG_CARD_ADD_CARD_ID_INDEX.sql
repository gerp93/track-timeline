-- Building a "never-played songs first" pile looks up each song in the card
-- event log by CARD_ID; without an index that is a full scan of the log per song.
ALTER TABLE TRACK_TIMELINE_LOG_CARD ADD INDEX IF NOT EXISTS IDX_LOG_CARD_CARD_ID (CARD_ID);
