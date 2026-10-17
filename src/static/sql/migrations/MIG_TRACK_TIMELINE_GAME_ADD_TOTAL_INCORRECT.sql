-- Running total of the on-turn player's incorrect placements across the whole
-- game; the counterpart of TOTAL_CORRECT.
ALTER TABLE TRACK_TIMELINE_GAME
    ADD COLUMN IF NOT EXISTS TOTAL_INCORRECT INT NOT NULL DEFAULT 0;
