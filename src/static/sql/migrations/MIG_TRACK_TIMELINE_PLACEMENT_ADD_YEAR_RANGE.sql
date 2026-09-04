-- Snapshot the year-window the turn player locked in, so a later steal
-- compares against the committed range rather than re-deriving it from a
-- timeline that may have changed (buys, etc.) during the steal window.
ALTER TABLE TRACK_TIMELINE_PLACEMENT
    ADD COLUMN IF NOT EXISTS RANGE_HAS_LOWER TINYINT(1) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS RANGE_LOWER INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS RANGE_HAS_UPPER TINYINT(1) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS RANGE_UPPER INT NOT NULL DEFAULT 0;
