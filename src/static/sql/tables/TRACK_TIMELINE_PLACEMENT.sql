-- Placements committed against the card currently in play, cleared when the
-- round resolves. Exactly one row has IS_CHALLENGE = 0 (the current player's
-- own placement, which opens the challenge window); every other row is a
-- challenger who spent a token.
--
-- CREATED_ON_DATE breaks ties at reveal: if the current player is wrong, the
-- earliest correct challenger takes the card.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_PLACEMENT(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    TRACK_TIMELINE_GAME_ID UUID NOT NULL,
    PLAYER_ID UUID NOT NULL,
    POSITION INT NOT NULL,
    IS_CHALLENGE BOOLEAN NOT NULL DEFAULT 0,
    PRIMARY KEY(ID),
    FOREIGN KEY(TRACK_TIMELINE_GAME_ID) REFERENCES TRACK_TIMELINE_GAME(ID) ON DELETE CASCADE,
    FOREIGN KEY(PLAYER_ID) REFERENCES PLAYER(ID) ON DELETE CASCADE,
    CONSTRAINT GAME_PLAYER_UNIQUE UNIQUE(TRACK_TIMELINE_GAME_ID, PLAYER_ID)
);
