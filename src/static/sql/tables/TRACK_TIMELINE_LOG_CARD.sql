-- Append-only log of card lifecycle events during play: 'drawn' when a card
-- becomes the song to place, 'discarded' when nobody claimed it, 'skipped' when
-- a player skipped it (typically a dead or region-locked video), 'bought' when
-- a player spent tokens to place it automatically without playing it, 'dealt'
-- when it was handed to a player as the first card of their timeline at the
-- start of a game. Only 'dealt' fills USER_ID (who received it) and POOL_SIZE
-- (how many songs were in that game's draw pile), so repeated starting cards
-- can be told apart from a small pile. No foreign keys, for the same reason as
-- TRACK_TIMELINE_LOG_PLACEMENT.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_LOG_CARD(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP(),
    CARD_ID UUID NOT NULL,
    EVENT_TYPE ENUM('drawn', 'discarded', 'skipped', 'bought', 'dealt') NOT NULL,
    USER_ID UUID NULL,
    POOL_SIZE INT NULL,
    PRIMARY KEY(ID),
    INDEX IDX_LOG_CARD_CARD_ID (CARD_ID)
);
