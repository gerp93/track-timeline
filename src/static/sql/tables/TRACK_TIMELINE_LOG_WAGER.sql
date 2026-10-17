-- Append-only log of exact-year wagers: the turn player stakes tokens on naming
-- a song's exact release year at lock-in, gets the stake back doubled when the
-- year is right (net +TOKENS) and loses it when it is wrong (net -TOKENS). No
-- foreign keys, for the same reason as TRACK_TIMELINE_LOG_PLACEMENT. RELEASE_YEAR
-- is snapshotted as the answer for that wager.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_LOG_WAGER(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP(),
    USER_ID UUID NOT NULL,
    CARD_ID UUID NOT NULL,
    EXACT_YEAR_GUESS INT NOT NULL,
    RELEASE_YEAR INT NOT NULL,
    TOKENS INT NOT NULL,
    IS_CORRECT TINYINT(1) NOT NULL,
    PRIMARY KEY(ID)
);
