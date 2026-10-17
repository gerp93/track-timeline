-- Whether a card's YouTube video was still playable last time an admin ran
-- "Check Videos". No row means never checked, distinct from checked-and-fine.
--
-- Deliberately its own table rather than columns on CARD: TR_AUDIT_CARD_UPDATE
-- fires on any UPDATE to CARD regardless of which columns changed, so writing
-- a check result there would insert an AUDIT_CARD row per card on every
-- routine check, with no authored-content change behind it.
-- DURATION_SECONDS rides along on the same check: videos.list costs one quota
-- unit per call no matter how many parts it requests, so the length is free
-- to fetch alongside availability. NULL means unknown -- never checked, or
-- YouTube reported no real length (live streams and unstarted premieres come
-- back as "P0D"). It is what lets the sample playback mode pick a random
-- start point that is guaranteed not to run off the end of the song.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_CARD_VIDEO_STATUS(
    CARD_ID UUID NOT NULL,
    AVAILABLE TINYINT(1) NOT NULL,
    DURATION_SECONDS INT NULL,
    CHECKED_ON_DATE DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP(),
    -- Set when an author changes the YouTube link; cleared on the next
    -- successful API check. Cards in this state stay on the dead-video admin
    -- list until re-validated, even if AVAILABLE was previously 1.
    AWAITING_VALIDATION TINYINT(1) NOT NULL DEFAULT 0,
    -- Playable but the wrong song. Stays on the dead-videos list until fixed
    -- or cleared by Mark OK / a successful API check.
    INCORRECT_VIDEO TINYINT(1) NOT NULL DEFAULT 0,
    -- What YouTube calls the video, learned on the same check (the snippet part
    -- rides the same one-unit videos.list call). YOUTUBE_TITLE_VIDEO_ID records
    -- which video id the title was read from, so a title is only trusted while
    -- it still matches CARD.YOUTUBE_VIDEO_ID -- a link edit orphans it without
    -- anything having to clear it. Drives the Library "Mismatches" tab.
    YOUTUBE_TITLE VARCHAR(255) NULL,
    YOUTUBE_CHANNEL VARCHAR(255) NULL,
    YOUTUBE_TITLE_VIDEO_ID VARCHAR(255) NULL,
    -- The video id an admin said "looks fine" about on the Mismatches tab. Like
    -- the title, it only counts while it equals the card's current video id.
    MISMATCH_OK_VIDEO_ID VARCHAR(255) NULL,
    PRIMARY KEY(CARD_ID),
    FOREIGN KEY(CARD_ID) REFERENCES CARD(ID) ON DELETE CASCADE
);
