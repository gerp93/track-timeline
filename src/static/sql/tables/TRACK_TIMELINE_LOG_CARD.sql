-- Append-only log of card lifecycle events during play: 'drawn' when a card
-- becomes the song to place, 'discarded' when nobody claimed it, 'skipped' when
-- a player skipped it (typically a dead or region-locked video), 'bought' when
-- a player spent tokens to place it automatically without playing it. No
-- foreign keys, for the same reason as TRACK_TIMELINE_LOG_PLACEMENT.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_LOG_CARD(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP(),
    CARD_ID UUID NOT NULL,
    EVENT_TYPE ENUM('drawn', 'discarded', 'skipped', 'bought') NOT NULL,
    PRIMARY KEY(ID)
);
