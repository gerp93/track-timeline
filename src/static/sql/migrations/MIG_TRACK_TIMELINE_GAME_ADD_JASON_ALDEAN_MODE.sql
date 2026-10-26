-- Per-lobby "Jason Aldean mode": adds an "easy, that's Jason Aldean" checkbox
-- beside the artist guess box.
ALTER TABLE TRACK_TIMELINE_GAME
    ADD COLUMN IF NOT EXISTS JASON_ALDEAN_MODE TINYINT(1) NOT NULL DEFAULT 0;
