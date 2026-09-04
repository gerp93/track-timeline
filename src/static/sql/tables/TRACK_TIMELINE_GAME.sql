-- One game per lobby. GAME_STATUS is the coarse lifecycle; ROUND_PHASE is where
-- the current card sits within a single round:
--
--   listening   song is playing. Anyone may guess the artist/title for a
--               token. Only the current player may commit a placement.
--   steal_join  the current player has committed a placement -- right or
--               wrong, not revealed yet. A short, server-timed window during
--               which exactly one other eligible player may claim the sole
--               steal attempt (STEALER_PLAYER_ID, claim only -- no cost yet).
--               Deliberately opens on every placement, not just a wrong one:
--               a stealer who could only ever be invited once the original
--               is already known to be wrong takes no real risk.
--   steal_turn  the player who claimed the steal gets a server-timed window
--               to place the card on their own timeline. Right takes the
--               card outright. Wrong or timed out falls back to the original
--               placement: if it was actually right after all, the original
--               player keeps it; otherwise the card is discarded. There is
--               no second claimant -- one steal attempt per round, win or
--               lose.
--   reveal      the answer is public; the server has already resolved who (if
--               anyone) takes the card.
--
-- PHASE_STARTED_ON_DATE stamps the transition into a timed phase (steal_join,
-- steal_turn) so both the client countdown and the server's own scheduled
-- timeout (a time.AfterFunc in the API layer) agree on when it started,
-- rather than each client independently starting a local timer on receipt.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_GAME(
    ID UUID NOT NULL DEFAULT UUID(),
    LOBBY_ID UUID NOT NULL,
    CREATED_ON_DATE DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP(),
    CURRENT_PLAYER_ID UUID NULL,
    GAME_STATUS ENUM('waiting', 'active', 'finished') NOT NULL DEFAULT 'waiting',
    ROUND_PHASE ENUM('listening', 'steal_join', 'steal_turn', 'reveal') NOT NULL DEFAULT 'listening',
    PHASE_STARTED_ON_DATE DATETIME(3) NULL,
    STEALER_PLAYER_ID UUID NULL,
    CARDS_TO_WIN INT NOT NULL DEFAULT 10,
    STARTING_TOKENS INT NOT NULL DEFAULT 2,
    -- Free-form title/artist guess economy for this lobby:
    --   off     no guess UI; no guess token awarded.
    --   both    both title and artist must be right for the token (default).
    --   title   only the song title is guessable / judged for the token.
    --   either  title or artist correct is enough for the token.
    GUESS_MODE ENUM('off', 'both', 'title', 'either') NOT NULL DEFAULT 'both',
    -- Fraction of authored title/artist words that must match (60/70/80/90).
    -- Local judge: that is the bar. Claude: that is the heuristic fallback
    -- if the API errors or the reply is unreadable. Never 100.
    GUESS_MATCH_PERCENT INT NOT NULL DEFAULT 60,
    -- How free-form guesses are judged:
    --   local   the built-in word matcher (uses GUESS_MATCH_PERCENT).
    --   claude  Anthropic Claude yes/no on intent; GUESS_MATCH_PERCENT is fallback.
    GUESS_JUDGE ENUM('local', 'claude') NOT NULL DEFAULT 'local',
    -- How much of each song the lobby hears:
    --   full    the whole song, from the top, until it ends.
    --   intro   the first CLIP_SECONDS seconds.
    --   sample  a random CLIP_SECONDS-second window from the middle of the
    --           song, never touching the first SampleLeadInSeconds (an intro
    --           gives the title away too easily) and always far enough from
    --           the end to run the full length. Needs a known duration, so a
    --           card whose length was never fetched falls back to 'intro'.
    PLAYBACK_MODE ENUM('full', 'intro', 'sample') NOT NULL DEFAULT 'sample',
    CLIP_SECONDS INT NOT NULL DEFAULT 20,
    -- The window actually chosen for the song currently in play, stamped when
    -- it is first played. Stored rather than recomputed because 'sample'
    -- picks at random: a paid replay has to be the same clip again, not a
    -- second roll of the dice. CLIP_END_SECONDS 0 means "to the end".
    CLIP_START_SECONDS INT NOT NULL DEFAULT 0,
    CLIP_END_SECONDS INT NOT NULL DEFAULT 0,
    -- Whether the player on turn has already spent a token to replay this
    -- round's clip. One replay per round, cleared on every turn advance.
    REPLAY_USED TINYINT(1) NOT NULL DEFAULT 0,
    WINNER_ID UUID NULL,
    PRIMARY KEY(ID),
    FOREIGN KEY(LOBBY_ID) REFERENCES LOBBY(ID) ON DELETE CASCADE,
    FOREIGN KEY(CURRENT_PLAYER_ID) REFERENCES PLAYER(ID) ON DELETE SET NULL,
    FOREIGN KEY(STEALER_PLAYER_ID) REFERENCES PLAYER(ID) ON DELETE SET NULL,
    FOREIGN KEY(WINNER_ID) REFERENCES USER(ID) ON DELETE SET NULL,
    CONSTRAINT LOBBY_UNIQUE UNIQUE(LOBBY_ID)
);
