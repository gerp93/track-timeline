-- The randomized turn order for one game. Rewritten every time the game starts,
-- so play order changes between games in the same lobby. Players who join
-- mid-game have no row and fall back to PLAYER.JOIN_ORDER at the end of the
-- rotation (see GetPlayers).
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_PLAYER_ORDER(
    ID UUID NOT NULL DEFAULT UUID(),
    TRACK_TIMELINE_GAME_ID UUID NOT NULL,
    PLAYER_ID UUID NOT NULL,
    TURN_ORDER INT NOT NULL,
    PRIMARY KEY(ID),
    FOREIGN KEY(TRACK_TIMELINE_GAME_ID) REFERENCES TRACK_TIMELINE_GAME(ID) ON DELETE CASCADE,
    FOREIGN KEY(PLAYER_ID) REFERENCES PLAYER(ID) ON DELETE CASCADE,
    CONSTRAINT GAME_PLAYER_UNIQUE UNIQUE(TRACK_TIMELINE_GAME_ID, PLAYER_ID)
);
