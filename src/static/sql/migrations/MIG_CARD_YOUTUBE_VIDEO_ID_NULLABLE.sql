-- Allow songs with no usable YouTube link (e.g. import with a malformed
-- videoId). NULL means "no link yet"; admins fix it from Dead Videos / Find.
ALTER TABLE CARD
    MODIFY COLUMN YOUTUBE_VIDEO_ID VARCHAR(32) NULL;
