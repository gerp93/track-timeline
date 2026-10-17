-- Running total of the on-turn player's correct placements across the whole
-- game, shown in the board header. Bumped when a round reveals (never at
-- lock-in, which would give the result away early); zeroed on game reset.
ALTER TABLE TRACK_TIMELINE_GAME
    ADD COLUMN IF NOT EXISTS TOTAL_CORRECT INT NOT NULL DEFAULT 0;
