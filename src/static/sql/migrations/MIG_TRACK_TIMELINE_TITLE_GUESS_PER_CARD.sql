-- Additive upgrade for databases created before guesses were kept per song and
-- judged together at the end of the round. On a fresh database
-- TRACK_TIMELINE_TITLE_GUESS.sql already carries all of this, so every clause is
-- a no-op; on an existing database it adds the columns, swaps the one-guess-
-- per-player unique key for one-per-player-per-song, and drops the old key.
-- The new key is added in the same statement as the drop, since the foreign key
-- on the game needs an index that starts with the game at every moment. One
-- statement, since the schema runner has no multi-statement support.
ALTER TABLE TRACK_TIMELINE_TITLE_GUESS
    ADD COLUMN IF NOT EXISTS CARD_ID UUID NULL,
    ADD COLUMN IF NOT EXISTS TITLE_GUESS VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS ARTIST_GUESS VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS LOCKED BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS JUDGED BOOLEAN NOT NULL DEFAULT FALSE,
    MODIFY COLUMN GUESS_TEXT VARCHAR(510) NOT NULL DEFAULT '',
    MODIFY COLUMN TITLE_CORRECT BOOLEAN NOT NULL DEFAULT FALSE,
    MODIFY COLUMN ARTIST_CORRECT BOOLEAN NOT NULL DEFAULT FALSE,
    ADD UNIQUE INDEX IF NOT EXISTS GAME_PLAYER_CARD_UNIQUE (TRACK_TIMELINE_GAME_ID, PLAYER_ID, CARD_ID),
    DROP INDEX IF EXISTS GAME_PLAYER_UNIQUE;
