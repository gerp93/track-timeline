-- Per-lobby guess judge: local word matcher or Claude intent.
ALTER TABLE TRACK_TIMELINE_GAME
    ADD COLUMN IF NOT EXISTS GUESS_JUDGE ENUM('local', 'claude') NOT NULL DEFAULT 'local';
