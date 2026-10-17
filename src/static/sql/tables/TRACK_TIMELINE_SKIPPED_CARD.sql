-- The songs skipped during the round in progress, oldest first. A skip does not
-- end a round: the guesses made on the skipped song are kept and judged along
-- with the song that is finally placed, and this is how the server knows a guess
-- tagged with a song that is no longer playing still belongs to this round.
-- Cleared when the round resolves, so a guess for a song from an earlier round
-- is refused.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_SKIPPED_CARD(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    TRACK_TIMELINE_GAME_ID UUID NOT NULL,
    CARD_ID UUID NOT NULL,
    PRIMARY KEY(ID),
    FOREIGN KEY(TRACK_TIMELINE_GAME_ID) REFERENCES TRACK_TIMELINE_GAME(ID) ON DELETE CASCADE,
    FOREIGN KEY(CARD_ID) REFERENCES CARD(ID) ON DELETE CASCADE,
    CONSTRAINT GAME_CARD_UNIQUE UNIQUE(TRACK_TIMELINE_GAME_ID, CARD_ID)
);
