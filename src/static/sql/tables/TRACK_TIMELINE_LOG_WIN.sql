-- Append-only log of completed games. No foreign keys, for the same reason as
-- TRACK_TIMELINE_LOG_PLACEMENT. IS_ROOM is 1 for a room-mode (in-person) game,
-- 0 for an online one, and NULL for games logged before the mode was recorded
-- (the room row is deleted with its lobby, so it cannot be recovered later).
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_LOG_WIN(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP(),
    USER_ID UUID NOT NULL,
    CARDS_TO_WIN INT NOT NULL,
    PLAYER_COUNT INT NOT NULL,
    IS_ROOM TINYINT(1) NULL,
    PRIMARY KEY(ID)
);
