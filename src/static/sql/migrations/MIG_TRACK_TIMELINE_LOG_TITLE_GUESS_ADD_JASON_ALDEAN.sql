-- Whether the artist half of this guess came from the "easy, that's Jason
-- Aldean" checkbox. Guesses logged before the mode existed are 0 (not used).
ALTER TABLE TRACK_TIMELINE_LOG_TITLE_GUESS
    ADD COLUMN IF NOT EXISTS JASON_ALDEAN TINYINT(1) NOT NULL DEFAULT 0;
