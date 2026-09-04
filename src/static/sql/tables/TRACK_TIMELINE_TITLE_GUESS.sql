-- One row per player per card round, recording their free-form artist/title
-- guess and how it was judged. The GAME_PLAYER_UNIQUE constraint is what
-- enforces "one guess per card" — a player cannot keep guessing until something
-- sticks. Cleared when the round resolves.
--
-- CREATED_ON_DATE breaks ties at reveal: the guess token goes to the
-- earliest-submitted qualifying guess under the lobby's guess mode, whether
-- or not that player is on turn.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_TITLE_GUESS(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    TRACK_TIMELINE_GAME_ID UUID NOT NULL,
    PLAYER_ID UUID NOT NULL,
    GUESS_TEXT VARCHAR(510) NOT NULL,
    TITLE_CORRECT BOOLEAN NOT NULL,
    ARTIST_CORRECT BOOLEAN NOT NULL,
    TITLE_MATCH_PERCENT INT NOT NULL DEFAULT 0,
    ARTIST_MATCH_PERCENT INT NOT NULL DEFAULT 0,
    TOKENS_AWARDED INT NOT NULL DEFAULT 0,
    PRIMARY KEY(ID),
    FOREIGN KEY(TRACK_TIMELINE_GAME_ID) REFERENCES TRACK_TIMELINE_GAME(ID) ON DELETE CASCADE,
    FOREIGN KEY(PLAYER_ID) REFERENCES PLAYER(ID) ON DELETE CASCADE,
    CONSTRAINT GAME_PLAYER_UNIQUE UNIQUE(TRACK_TIMELINE_GAME_ID, PLAYER_ID)
);
