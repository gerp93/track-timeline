-- Whether the artist half of this guess came from the "easy, that's Jason
-- Aldean" checkbox rather than being typed.
ALTER TABLE TRACK_TIMELINE_TITLE_GUESS
    ADD COLUMN IF NOT EXISTS JASON_ALDEAN TINYINT(1) NOT NULL DEFAULT 0;
