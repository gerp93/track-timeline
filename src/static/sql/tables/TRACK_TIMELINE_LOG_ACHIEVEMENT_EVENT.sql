-- Append-only log of the in-game moments the achievements are built from that no
-- other log records: a steal that took a card from someone, a guess a typo
-- slipped past, a wager that took a player's last tokens, a game won in record
-- time. EVENT_TYPE names the moment (see the Ev* constants in
-- database/achievements-events.go); VALUE carries a number when it has one (a
-- year span, a count of seconds, a token balance) and is 1 otherwise. GAME_ID
-- lets a game-scoped question ("was this player ever three cards behind in the
-- game they won?") be asked of the log; it is NULL for moments outside a game.
-- Recorded only from when each was added, and never inferred for earlier play.
-- No foreign keys, for the same reason as TRACK_TIMELINE_LOG_PLACEMENT.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_LOG_ACHIEVEMENT_EVENT(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP(),
    USER_ID UUID NOT NULL,
    GAME_ID UUID NULL,
    EVENT_TYPE VARCHAR(40) NOT NULL,
    VALUE INT NOT NULL DEFAULT 1,
    PRIMARY KEY(ID),
    INDEX IDX_LOG_ACHIEVEMENT_EVENT_USER (USER_ID, EVENT_TYPE),
    INDEX IDX_LOG_ACHIEVEMENT_EVENT_GAME (GAME_ID, EVENT_TYPE)
);
