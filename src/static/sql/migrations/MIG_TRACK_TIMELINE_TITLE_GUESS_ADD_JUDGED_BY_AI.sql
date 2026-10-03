-- Additive upgrade for databases created before guesses recorded whether the
-- AI judge (as opposed to the local word matcher) decided them. On a fresh
-- database TRACK_TIMELINE_TITLE_GUESS.sql already includes this column, so
-- this is a no-op; on an existing database it adds the column.
ALTER TABLE TRACK_TIMELINE_TITLE_GUESS ADD COLUMN IF NOT EXISTS JUDGED_BY_AI BOOLEAN NOT NULL DEFAULT FALSE;
