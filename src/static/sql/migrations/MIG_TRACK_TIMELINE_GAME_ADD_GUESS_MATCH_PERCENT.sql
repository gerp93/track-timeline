-- How much of a title/artist must match to count as right (60/70/80/90).
-- On a fresh database TRACK_TIMELINE_GAME.sql already includes the column.
ALTER TABLE TRACK_TIMELINE_GAME
    ADD COLUMN IF NOT EXISTS GUESS_MATCH_PERCENT INT NOT NULL DEFAULT 60;
