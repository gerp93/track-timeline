-- One row per player per card round, recording their free-form artist/title
-- guess and how it was judged. The GAME_PLAYER_UNIQUE constraint is what
-- enforces "one guess per card" — a player cannot keep guessing until something
-- sticks. Cleared when the round resolves.
--
-- Every qualifying guess earns its own token at reveal (database.
-- AwardGuessTokens) — there is no race for a single token, so CREATED_ON_DATE
-- here is just submit-order recency for chat, not an economic tiebreaker.
--
-- JUDGED_BY_AI records whether the configured AI judge (as opposed to the
-- local word matcher, including a Claude call that errored and fell back)
-- actually decided this guess — guess.Verdict.ByAI is not otherwise
-- persisted, and the reveal chat line / re-rendered "already guessed" fragment
-- need it later to skip match-percent phrasing for an AI verdict.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_TITLE_GUESS(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    TRACK_TIMELINE_GAME_ID UUID NOT NULL,
    PLAYER_ID UUID NOT NULL,
    GUESS_TEXT VARCHAR(510) NOT NULL,
    TITLE_CORRECT BOOLEAN NOT NULL,
    ARTIST_CORRECT BOOLEAN NOT NULL,
    TITLE_MATCH_PERCENT INT NOT NULL DEFAULT 0,
    ARTIST_MATCH_PERCENT INT NOT NULL DEFAULT 0,
    JUDGED_BY_AI BOOLEAN NOT NULL DEFAULT FALSE,
    TOKENS_AWARDED INT NOT NULL DEFAULT 0,
    PRIMARY KEY(ID),
    FOREIGN KEY(TRACK_TIMELINE_GAME_ID) REFERENCES TRACK_TIMELINE_GAME(ID) ON DELETE CASCADE,
    FOREIGN KEY(PLAYER_ID) REFERENCES PLAYER(ID) ON DELETE CASCADE,
    CONSTRAINT GAME_PLAYER_UNIQUE UNIQUE(TRACK_TIMELINE_GAME_ID, PLAYER_ID)
);
