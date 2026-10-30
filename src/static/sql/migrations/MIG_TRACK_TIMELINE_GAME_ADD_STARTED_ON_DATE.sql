-- When the game was started, stamped by StartGame and cleared by ResetGame. The
-- game-length achievements and "did this player miss anything this game" read
-- it. NULL for a game started before this column existed.
ALTER TABLE TRACK_TIMELINE_GAME
    ADD COLUMN IF NOT EXISTS STARTED_ON_DATE DATETIME NULL;
