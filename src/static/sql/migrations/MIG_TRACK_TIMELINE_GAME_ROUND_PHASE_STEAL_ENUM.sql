-- The old single 'challenge' phase (any number of independent challengers,
-- judged silently in commit order) is replaced by a visible, sequential,
-- timed steal queue: 'steal_join' (a short window to join the queue) then
-- 'steal_turn' (one queued player's turn to attempt it). MODIFY COLUMN is
-- idempotent on its own -- safe to always rerun. Must run after
-- MIG_TRACK_TIMELINE_GAME_CLEAR_LEGACY_CHALLENGE_PHASE.sql, which resets any
-- existing 'challenge' rows first.
ALTER TABLE TRACK_TIMELINE_GAME MODIFY COLUMN ROUND_PHASE ENUM('listening', 'steal_join', 'steal_turn', 'reveal') NOT NULL DEFAULT 'listening';
