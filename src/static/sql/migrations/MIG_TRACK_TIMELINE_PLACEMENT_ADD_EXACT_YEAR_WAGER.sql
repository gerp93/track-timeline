-- Exact-year wager details on the turn player's placement, announced at reveal
-- so the steal window is not spoiled by the year digits.
ALTER TABLE TRACK_TIMELINE_PLACEMENT
    ADD COLUMN IF NOT EXISTS EXACT_YEAR_GUESS INT NULL,
    ADD COLUMN IF NOT EXISTS YEAR_WAGER INT NOT NULL DEFAULT 0;
