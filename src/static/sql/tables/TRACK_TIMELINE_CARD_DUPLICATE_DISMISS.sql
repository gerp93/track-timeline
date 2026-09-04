-- Admin-confirmed false positives for the library duplicates finder.
-- CARD_ID_A / CARD_ID_B are always stored ordered (A < B as UUID bytes) so
-- each undirected pair is a single row. Cascade away when either card goes.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_CARD_DUPLICATE_DISMISS(
    CARD_ID_A UUID NOT NULL,
    CARD_ID_B UUID NOT NULL,
    CREATED_ON_DATE DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP(),
    PRIMARY KEY(CARD_ID_A, CARD_ID_B),
    FOREIGN KEY(CARD_ID_A) REFERENCES CARD(ID) ON DELETE CASCADE,
    FOREIGN KEY(CARD_ID_B) REFERENCES CARD(ID) ON DELETE CASCADE
);
