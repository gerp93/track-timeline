-- Append-only record of every line the GAME writes to a lobby's chat (turn
-- results, guess verdicts, token changes, wagers, buys, steals, challenges, the
-- room host's "sat down" lines) -- not what players type. Kept so a statistic
-- nobody thought of yet can still be rebuilt, at least roughly, from what the
-- table was told at the time. MESSAGE is exactly the text sent, colour markup
-- (<blue>, <green>, <red>) included. SEQ is a total order across the table:
-- CREATED_ON_DATE can tie within a millisecond, SEQ never does. GAME_ID and
-- IS_ROOM are snapshotted because the lobby, game and room rows are deleted when
-- the lobby closes; GAME_ID is NULL for a line written before a game existed.
-- No foreign keys, for the same reason as TRACK_TIMELINE_LOG_PLACEMENT.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_LOG_CHAT(
    ID UUID NOT NULL DEFAULT UUID(),
    SEQ BIGINT NOT NULL AUTO_INCREMENT,
    CREATED_ON_DATE DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    LOBBY_ID UUID NOT NULL,
    GAME_ID UUID NULL,
    IS_ROOM TINYINT(1) NOT NULL DEFAULT 0,
    MESSAGE TEXT NOT NULL,
    PRIMARY KEY(ID),
    UNIQUE KEY TRACK_TIMELINE_LOG_CHAT_SEQ_UQ(SEQ),
    INDEX TRACK_TIMELINE_LOG_CHAT_LOBBY_IDX(LOBBY_ID, SEQ),
    INDEX TRACK_TIMELINE_LOG_CHAT_GAME_IDX(GAME_ID, SEQ)
);
