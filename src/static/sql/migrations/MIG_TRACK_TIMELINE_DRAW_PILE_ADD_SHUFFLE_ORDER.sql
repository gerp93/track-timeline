-- Fixed random draw order assigned once when the pile is built, so every
-- draw/deal pulls the next card in that order rather than re-rolling
-- ORDER BY RAND() each time. On a fresh database TRACK_TIMELINE_DRAW_PILE.sql
-- already includes SHUFFLE_ORDER; this is the additive upgrade for older DBs.
ALTER TABLE TRACK_TIMELINE_DRAW_PILE
    ADD COLUMN IF NOT EXISTS SHUFFLE_ORDER INT NOT NULL DEFAULT 0;
