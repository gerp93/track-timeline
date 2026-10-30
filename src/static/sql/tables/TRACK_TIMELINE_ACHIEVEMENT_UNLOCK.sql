-- One row per achievement tier a player has unlocked, written once and never
-- changed: a tiered achievement that jumps two tiers at once gets a row for each,
-- and a single achievement is tier 1. BACKFILLED is 1 for a tier granted
-- silently from the existing history (when achievements were first switched on,
-- or when a new one was added) rather than earned in front of the table, so the
-- trophy case can say the date is not known. No foreign keys, for the same
-- reason as TRACK_TIMELINE_LOG_PLACEMENT: an unlock outlives the lobby and game
-- it happened in.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_ACHIEVEMENT_UNLOCK(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP(),
    USER_ID UUID NOT NULL,
    ACHIEVEMENT_KEY VARCHAR(48) NOT NULL,
    TIER INT NOT NULL,
    BACKFILLED TINYINT(1) NOT NULL DEFAULT 0,
    PRIMARY KEY(ID),
    CONSTRAINT TRACK_TIMELINE_ACHIEVEMENT_UNLOCK_UQ UNIQUE(USER_ID, ACHIEVEMENT_KEY, TIER)
);
