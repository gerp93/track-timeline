-- Match CARD: audited copies may have had no YouTube link.
ALTER TABLE AUDIT_CARD
    MODIFY COLUMN YOUTUBE_VIDEO_ID VARCHAR(32) NULL;
