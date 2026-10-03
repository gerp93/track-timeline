-- Whether the game is between rounds: a round has resolved and the next song has
-- not been started yet. The only time a challenge can be raised.
ALTER TABLE TRACK_TIMELINE_GAME
    ADD COLUMN IF NOT EXISTS BETWEEN_ROUNDS TINYINT(1) NOT NULL DEFAULT 0;
