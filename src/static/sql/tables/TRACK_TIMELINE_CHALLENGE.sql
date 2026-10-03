-- A player's claim that the game wronged them -- an AI judge that marked a right
-- answer wrong, a glitch, a video that does not match its song, a bad year --
-- and what they say they are owed: a free card, or a number of tokens. It can
-- only be raised between rounds (see BETWEEN_ROUNDS on TRACK_TIMELINE_GAME) and
-- the other players vote on it; a strict majority of them upholds it and the
-- challenger is paid what they asked for.
--
-- STATUS:
--   open       being voted on right now; nothing else in the game may happen
--   upheld     the majority agreed and the claim was paid
--   rejected   the vote went against the challenger, who is now out of
--              challenges for the rest of the game (a rejected row IS the
--              record of that; there is no separate counter)
--   withdrawn  the challenger took it back; no penalty
--   void       it passed but could no longer be paid (a card that would now
--              reach the winning count); no penalty
--
-- OPEN_GAME_ID holds the game's id while the challenge is open and NULL once it
-- is closed. Its UNIQUE constraint is what guarantees at most one open
-- challenge per game, since NULLs never collide.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_CHALLENGE(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    TRACK_TIMELINE_GAME_ID UUID NOT NULL,
    PLAYER_ID UUID NOT NULL,
    KIND ENUM('card', 'tokens') NOT NULL,
    TOKENS INT NOT NULL DEFAULT 0,
    REASON VARCHAR(600) NOT NULL,
    STATUS ENUM('open', 'upheld', 'rejected', 'withdrawn', 'void') NOT NULL DEFAULT 'open',
    OPEN_GAME_ID UUID NULL,
    PRIMARY KEY(ID),
    FOREIGN KEY(TRACK_TIMELINE_GAME_ID) REFERENCES TRACK_TIMELINE_GAME(ID) ON DELETE CASCADE,
    FOREIGN KEY(PLAYER_ID) REFERENCES PLAYER(ID) ON DELETE CASCADE,
    CONSTRAINT ONE_OPEN_PER_GAME UNIQUE(OPEN_GAME_ID)
);
