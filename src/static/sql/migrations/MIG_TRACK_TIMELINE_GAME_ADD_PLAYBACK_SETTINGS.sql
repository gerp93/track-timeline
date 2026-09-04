-- Additive upgrade for databases created before per-lobby playback settings.
-- On a fresh database TRACK_TIMELINE_GAME.sql already includes these columns,
-- so this is a no-op; on an existing database it adds them. ADD COLUMN IF NOT
-- EXISTS is idempotent -- safe to always rerun. One statement, since the
-- schema runner has no multi-statement support.
ALTER TABLE TRACK_TIMELINE_GAME
    ADD COLUMN IF NOT EXISTS PLAYBACK_MODE ENUM('full', 'intro', 'sample') NOT NULL DEFAULT 'sample',
    ADD COLUMN IF NOT EXISTS CLIP_SECONDS INT NOT NULL DEFAULT 20,
    ADD COLUMN IF NOT EXISTS REPLAY_USED TINYINT(1) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS CLIP_START_SECONDS INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS CLIP_END_SECONDS INT NOT NULL DEFAULT 0;
