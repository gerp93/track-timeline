-- Zero or more inclusive [FROM_YEAR, TO_YEAR] era filters for one game. No rows
-- means every year is allowed. A card qualifies if its release year falls in at
-- least one range.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_YEAR_RANGE(
    ID UUID NOT NULL DEFAULT UUID(),
    TRACK_TIMELINE_GAME_ID UUID NOT NULL,
    FROM_YEAR INT NOT NULL,
    TO_YEAR INT NOT NULL,
    PRIMARY KEY(ID),
    FOREIGN KEY(TRACK_TIMELINE_GAME_ID) REFERENCES TRACK_TIMELINE_GAME(ID) ON DELETE CASCADE
);
