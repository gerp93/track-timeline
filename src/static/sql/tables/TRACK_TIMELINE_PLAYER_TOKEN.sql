-- Token balance per player per game. Tokens are earned by correctly naming a
-- song's title and/or artist and spent to challenge another player's placement.
-- A row is created lazily the first time a player's balance is read or changed;
-- absence means the game's STARTING_TOKENS value.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_PLAYER_TOKEN(
    ID UUID NOT NULL DEFAULT UUID(),
    TRACK_TIMELINE_GAME_ID UUID NOT NULL,
    PLAYER_ID UUID NOT NULL,
    TOKEN_COUNT INT NOT NULL DEFAULT 0,
    PRIMARY KEY(ID),
    FOREIGN KEY(TRACK_TIMELINE_GAME_ID) REFERENCES TRACK_TIMELINE_GAME(ID) ON DELETE CASCADE,
    FOREIGN KEY(PLAYER_ID) REFERENCES PLAYER(ID) ON DELETE CASCADE,
    CONSTRAINT GAME_PLAYER_UNIQUE UNIQUE(TRACK_TIMELINE_GAME_ID, PLAYER_ID)
);
