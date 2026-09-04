-- IS_CHALLENGE is obsolete: the old multi-challenger free-for-all is replaced
-- by the steal queue (TRACK_TIMELINE_STEAL_QUEUE), and this table now only
-- ever holds the turn player's own placement. DROP COLUMN IF EXISTS is
-- idempotent -- a no-op once the column is gone, safe to always rerun.
ALTER TABLE TRACK_TIMELINE_PLACEMENT DROP COLUMN IF EXISTS IS_CHALLENGE;
