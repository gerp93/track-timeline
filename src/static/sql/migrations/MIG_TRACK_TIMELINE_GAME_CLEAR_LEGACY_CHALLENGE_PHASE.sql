-- Existing games mid-round during this deploy may have ROUND_PHASE =
-- 'challenge', a value the next migration's enum removes. Reset them to
-- 'listening' first -- safe, since placement/steal state is re-derived from
-- the draw pile and current card, not carried in this column. Must run
-- before MIG_TRACK_TIMELINE_GAME_ROUND_PHASE_STEAL_ENUM.sql.
UPDATE TRACK_TIMELINE_GAME SET ROUND_PHASE = 'listening' WHERE ROUND_PHASE = 'challenge';
