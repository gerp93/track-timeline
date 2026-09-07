-- Room-mode session: a seatless host TV plus phone seats. Backed by a normal
-- LOBBY + TRACK_TIMELINE_GAME so the shared rules engine stays untouched; this
-- row marks the lobby as room-mode and holds the host token / pause flag.
-- Remote lobby search excludes any lobby that has a matching row here.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_ROOM(
    ID UUID NOT NULL DEFAULT UUID(),
    LOBBY_ID UUID NOT NULL,
    CODE VARCHAR(8) NOT NULL,
    HOST_TOKEN CHAR(64) NOT NULL,
    CREATOR_USER_ID UUID NOT NULL,
    IS_PAUSED TINYINT(1) NOT NULL DEFAULT 0,
    CREATED_ON_DATE DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP(),
    PRIMARY KEY(ID),
    UNIQUE KEY TRACK_TIMELINE_ROOM_CODE_UQ(CODE),
    UNIQUE KEY TRACK_TIMELINE_ROOM_LOBBY_UQ(LOBBY_ID),
    FOREIGN KEY(LOBBY_ID) REFERENCES LOBBY(ID) ON DELETE CASCADE,
    FOREIGN KEY(CREATOR_USER_ID) REFERENCES USER(ID) ON DELETE CASCADE
);
