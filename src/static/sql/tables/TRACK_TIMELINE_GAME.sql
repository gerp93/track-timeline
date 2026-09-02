-- One game per lobby. GAME_STATUS is the coarse lifecycle; ROUND_PHASE is where
-- the current card sits within a single round:
--
--   listening  song is playing. Anyone may guess the artist/title for a token.
--              Only the current player may commit a placement.
--   challenge  the current player has committed. Everyone else holding a token
--              may spend one to place the same card in their own timeline.
--   reveal     the answer is public; the server has already resolved who (if
--              anyone) takes the card.
--
-- CHALLENGE_OPENED_ON_DATE stamps the transition into 'challenge' so the window
-- can expire on its own if a player who could challenge simply never acts.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_GAME(
    ID UUID NOT NULL DEFAULT UUID(),
    LOBBY_ID UUID NOT NULL,
    CREATED_ON_DATE DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP(),
    CURRENT_PLAYER_ID UUID NULL,
    GAME_STATUS ENUM('waiting', 'active', 'finished') NOT NULL DEFAULT 'waiting',
    ROUND_PHASE ENUM('listening', 'challenge', 'reveal') NOT NULL DEFAULT 'listening',
    CHALLENGE_OPENED_ON_DATE DATETIME NULL,
    CARDS_TO_WIN INT NOT NULL DEFAULT 10,
    STARTING_TOKENS INT NOT NULL DEFAULT 2,
    WINNER_ID UUID NULL,
    PRIMARY KEY(ID),
    FOREIGN KEY(LOBBY_ID) REFERENCES LOBBY(ID) ON DELETE CASCADE,
    FOREIGN KEY(CURRENT_PLAYER_ID) REFERENCES PLAYER(ID) ON DELETE SET NULL,
    FOREIGN KEY(WINNER_ID) REFERENCES USER(ID) ON DELETE SET NULL,
    CONSTRAINT LOBBY_UNIQUE UNIQUE(LOBBY_ID)
);
