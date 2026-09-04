-- Wrong song linked (playable but incorrect). Kept on the dead-videos list
-- separately from unreachable videos. Cleared by API check or Mark OK.
ALTER TABLE TRACK_TIMELINE_CARD_VIDEO_STATUS
    ADD COLUMN IF NOT EXISTS INCORRECT_VIDEO TINYINT(1) NOT NULL DEFAULT 0;
