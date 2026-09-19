-- Claude always judges guesses now, with the word matcher only as its fallback
-- at a fixed bar, so neither the per-lobby judge nor the match percent is a
-- setting any more. A no-op on a database that never had, or has already
-- dropped, the columns.
ALTER TABLE TRACK_TIMELINE_GAME
    DROP COLUMN IF EXISTS GUESS_MATCH_PERCENT,
    DROP COLUMN IF EXISTS GUESS_JUDGE;
