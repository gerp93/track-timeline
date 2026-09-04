-- Additive upgrade for databases created before guesses recorded an artist
-- match percentage. On a fresh database TRACK_TIMELINE_TITLE_GUESS.sql already
-- includes this column, so this is a no-op; on an existing database it adds
-- the column. Single idempotent statement -- see the sibling
-- MIG_..._ADD_MATCH_PERCENT.sql for the title column.
ALTER TABLE TRACK_TIMELINE_TITLE_GUESS ADD COLUMN IF NOT EXISTS ARTIST_MATCH_PERCENT INT NOT NULL DEFAULT 0;
