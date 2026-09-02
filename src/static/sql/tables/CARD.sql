-- One song. TITLE, ARTIST and RELEASE_YEAR are the answer and must never reach
-- a client before the round's reveal; YOUTUBE_VIDEO_ID necessarily does reach
-- clients, because that is what plays the audio.
--
-- RELEASE_YEAR NULL means authored-but-incomplete: the card is silently
-- excluded from every draw pile rather than erroring at game start.
--
-- Uniqueness is on the video ID, not the title: the same recording listed under
-- two spellings is still one card, and two different recordings of the same
-- song are legitimately two cards.
CREATE TABLE IF NOT EXISTS CARD(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP(),
    CHANGED_ON_DATE DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP(),
    DECK_ID UUID NOT NULL,
    YOUTUBE_VIDEO_ID VARCHAR(32) NOT NULL,
    START_OFFSET_SECONDS INT NOT NULL DEFAULT 0,
    TITLE VARCHAR(510) NOT NULL,
    ARTIST VARCHAR(510) NOT NULL,
    RELEASE_YEAR INT NULL,
    -- Category is a soft reference into TRACK_TIMELINE_CATEGORY; integrity is
    -- enforced in the Go layer (required on create/edit, reassigned before a
    -- category is deleted) rather than a DB FK.
    CATEGORY_ID UUID NULL,
    PRIMARY KEY(ID),
    FOREIGN KEY(DECK_ID) REFERENCES DECK(ID) ON DELETE CASCADE,
    CONSTRAINT DECK_VIDEO_UNIQUE UNIQUE(DECK_ID, YOUTUBE_VIDEO_ID)
);
