-- Additive upgrade for databases created before the steal race redesign. On a
-- fresh database TRACK_TIMELINE_GAME.sql already includes this column, so
-- this is a no-op; on an existing database it adds it. ADD COLUMN IF NOT
-- EXISTS is idempotent -- safe to always rerun. The foreign key can't be
-- added in the same idempotent statement (MariaDB has no "ADD CONSTRAINT IF
-- NOT EXISTS"), so this column is left without one -- integrity is enforced
-- in the Go layer instead, the same way CARD.CATEGORY_ID already is.
ALTER TABLE TRACK_TIMELINE_GAME ADD COLUMN IF NOT EXISTS STEALER_PLAYER_ID UUID NULL;
