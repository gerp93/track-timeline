-- Append-only log of every placement attempt, challenge or not. Deliberately
-- has NO foreign keys: the framework deletes a LOBBY when its last websocket
-- client disconnects, cascading the game and its player rows away, but stats
-- must survive that. Joined back to CARD/USER by ID at query time, so a deleted
-- card simply drops out of the reports. RELEASE_YEAR is snapshotted because it
-- is the answer for that attempt, keeping era breakdowns stable even if the
-- card is later edited.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_LOG_PLACEMENT(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP(),
    USER_ID UUID NOT NULL,
    CARD_ID UUID NOT NULL,
    RELEASE_YEAR INT NOT NULL,
    IS_CHALLENGE BOOLEAN NOT NULL,
    IS_CORRECT BOOLEAN NOT NULL,
    PRIMARY KEY(ID)
);
