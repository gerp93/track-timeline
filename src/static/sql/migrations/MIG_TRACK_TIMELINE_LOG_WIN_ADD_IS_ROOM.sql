-- Whether a finished game was played in room mode (in person) or online. NULL
-- for games logged before this column existed; the stats pages report those as
-- "not recorded" rather than guessing.
ALTER TABLE TRACK_TIMELINE_LOG_WIN
    ADD COLUMN IF NOT EXISTS IS_ROOM TINYINT(1) NULL;
