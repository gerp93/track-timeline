-- One row per player per song per round, recording their free-form artist/title
-- guess. A round can span several songs (every song skipped before the one that
-- is finally placed), so the row is keyed by CARD_ID as well: a guess made on a
-- skipped song stays on file against that song and is still judged, and a guess
-- for one song can never be mistaken for a guess on the next.
--
-- A row is first a draft (LOCKED = 0): the browser saves whatever the player has
-- typed as they type it, so a song skipped or placed before they pressed Guess
-- still gets credit for what was in the boxes. Pressing Guess locks it (LOCKED =
-- 1), after which it can no longer be changed.
--
-- Nothing is judged as it arrives. When the round resolves, every row for every
-- song in the round is judged together, one request per song so the judge sees
-- everything said about that song at once (database.JudgeRoundGuesses). JUDGED
-- flips then, and the verdict columns are only meaningful once it has. Tokens
-- are paid at that moment, and TOKENS_AWARDED records what each guess paid out.
-- Cleared when the round resolves.
--
-- JUDGED_BY_AI records whether the configured AI judge (as opposed to the
-- local word matcher, including a Claude call that errored and fell back)
-- actually decided this guess — guess.Verdict.ByAI is not otherwise
-- persisted, and the reveal chat line needs it to skip match-percent phrasing
-- for an AI verdict.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_TITLE_GUESS(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    TRACK_TIMELINE_GAME_ID UUID NOT NULL,
    PLAYER_ID UUID NOT NULL,
    CARD_ID UUID NOT NULL,
    TITLE_GUESS VARCHAR(255) NOT NULL DEFAULT '',
    ARTIST_GUESS VARCHAR(255) NOT NULL DEFAULT '',
    GUESS_TEXT VARCHAR(510) NOT NULL DEFAULT '',
    LOCKED BOOLEAN NOT NULL DEFAULT FALSE,
    JUDGED BOOLEAN NOT NULL DEFAULT FALSE,
    TITLE_CORRECT BOOLEAN NOT NULL DEFAULT FALSE,
    ARTIST_CORRECT BOOLEAN NOT NULL DEFAULT FALSE,
    TITLE_MATCH_PERCENT INT NOT NULL DEFAULT 0,
    ARTIST_MATCH_PERCENT INT NOT NULL DEFAULT 0,
    JUDGED_BY_AI BOOLEAN NOT NULL DEFAULT FALSE,
    TOKENS_AWARDED INT NOT NULL DEFAULT 0,
    PRIMARY KEY(ID),
    FOREIGN KEY(TRACK_TIMELINE_GAME_ID) REFERENCES TRACK_TIMELINE_GAME(ID) ON DELETE CASCADE,
    FOREIGN KEY(PLAYER_ID) REFERENCES PLAYER(ID) ON DELETE CASCADE,
    FOREIGN KEY(CARD_ID) REFERENCES CARD(ID) ON DELETE CASCADE,
    CONSTRAINT GAME_PLAYER_CARD_UNIQUE UNIQUE(TRACK_TIMELINE_GAME_ID, PLAYER_ID, CARD_ID)
);
