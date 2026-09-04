-- The turn player's own committed placement against the card currently in
-- play, cleared when the round resolves. At most one row per round now -- a
-- stealer's attempt is judged and resolved immediately rather than recorded
-- here. EXACT_YEAR_GUESS / YEAR_WAGER record an optional exact-year wager so
-- the lobby chat can announce it at reveal without leaking the year before
-- the steal window. RANGE_* snapshots the year window at lock-in so a steal
-- compares against that committed range, not a re-derived one.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_PLACEMENT(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    TRACK_TIMELINE_GAME_ID UUID NOT NULL,
    PLAYER_ID UUID NOT NULL,
    POSITION INT NOT NULL,
    EXACT_YEAR_GUESS INT NULL,
    YEAR_WAGER INT NOT NULL DEFAULT 0,
    RANGE_HAS_LOWER TINYINT(1) NOT NULL DEFAULT 0,
    RANGE_LOWER INT NOT NULL DEFAULT 0,
    RANGE_HAS_UPPER TINYINT(1) NOT NULL DEFAULT 0,
    RANGE_UPPER INT NOT NULL DEFAULT 0,
    PRIMARY KEY(ID),
    FOREIGN KEY(TRACK_TIMELINE_GAME_ID) REFERENCES TRACK_TIMELINE_GAME(ID) ON DELETE CASCADE,
    FOREIGN KEY(PLAYER_ID) REFERENCES PLAYER(ID) ON DELETE CASCADE,
    CONSTRAINT GAME_PLAYER_UNIQUE UNIQUE(TRACK_TIMELINE_GAME_ID, PLAYER_ID)
);
