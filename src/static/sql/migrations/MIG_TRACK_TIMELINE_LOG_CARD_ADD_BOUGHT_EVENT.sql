-- Additive upgrade for databases created before the "buy a card" action
-- existed. On a fresh database TRACK_TIMELINE_LOG_CARD.sql already includes
-- 'bought' in the enum, so this is a no-op; on an existing database it adds
-- it. MODIFY COLUMN is idempotent on its own -- no guard needed, re-running
-- it against an enum that already includes 'bought' is a no-op.
ALTER TABLE TRACK_TIMELINE_LOG_CARD MODIFY COLUMN EVENT_TYPE ENUM('drawn', 'discarded', 'skipped', 'bought') NOT NULL;
