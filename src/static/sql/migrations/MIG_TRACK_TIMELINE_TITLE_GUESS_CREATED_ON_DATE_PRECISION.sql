-- Bumps CREATED_ON_DATE to microsecond precision so guesses submitted in the
-- same second still order correctly for the "earliest correct guess wins the
-- token" rule. On a fresh database TRACK_TIMELINE_TITLE_GUESS.sql already
-- creates the column at this precision, so this is a no-op there; on an
-- existing database it upgrades the column in place. MODIFY COLUMN is
-- idempotent on its own -- no IF EXISTS guard needed, re-running it against a
-- column already at this precision is a no-op.
ALTER TABLE TRACK_TIMELINE_TITLE_GUESS MODIFY COLUMN CREATED_ON_DATE DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6);
