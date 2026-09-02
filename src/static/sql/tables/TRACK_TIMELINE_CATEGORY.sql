-- Admin-managed genre list. Cards reference a category by ID, but as a soft
-- reference rather than a foreign key (see CARD.sql) so the column could be
-- added to an existing database in one idempotent ALTER.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_CATEGORY(
    ID UUID NOT NULL DEFAULT UUID(),
    CREATED_ON_DATE DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP(),
    NAME VARCHAR(255) NOT NULL,
    PRIMARY KEY(ID),
    CONSTRAINT NAME_UNIQUE UNIQUE(NAME)
);
