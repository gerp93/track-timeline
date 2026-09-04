-- Additive upgrade for databases created before guesses recorded a title match
-- percentage. On a fresh database TRACK_TIMELINE_TITLE_GUESS.sql already
-- includes this column, so this is a no-op; on an existing database it adds
-- the column. Single idempotent statement (the schema runner executes one
-- statement per file, no multiStatements) -- see the sibling
-- MIG_..._ADD_ARTIST_MATCH_PERCENT.sql for the other column.
ALTER TABLE TRACK_TIMELINE_TITLE_GUESS ADD COLUMN IF NOT EXISTS TITLE_MATCH_PERCENT INT NOT NULL DEFAULT 0;
