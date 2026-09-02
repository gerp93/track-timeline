-- Append-only log of every artist/title guess and its verdict. No foreign keys,
-- for the same reason as TRACK_TIMELINE_LOG_PLACEMENT. GUESS_TEXT is retained
-- so a judge implementation can be evaluated after the fact against what people
-- actually typed.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_LOG_TITLE_GUESS(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP(),
    USER_ID UUID NOT NULL,
    CARD_ID UUID NOT NULL,
    GUESS_TEXT VARCHAR(510) NOT NULL,
    TITLE_CORRECT BOOLEAN NOT NULL,
    ARTIST_CORRECT BOOLEAN NOT NULL,
    PRIMARY KEY(ID)
);
