-- Per-lobby "Jason Aldeen mode": adds an "easy, that's Jason Aldeen" checkbox
-- beside the artist guess box.
ALTER TABLE TRACK_TIMELINE_GAME
    ADD COLUMN IF NOT EXISTS JASON_ALDEEN_MODE TINYINT(1) NOT NULL DEFAULT 0;
