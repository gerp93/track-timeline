-- Guessing has one rule now (a token for the right title and another for the
-- right artist), so the per-lobby mode is gone. A no-op on a database that never
-- had, or has already dropped, the column.
ALTER TABLE TRACK_TIMELINE_GAME DROP COLUMN IF EXISTS GUESS_MODE;
