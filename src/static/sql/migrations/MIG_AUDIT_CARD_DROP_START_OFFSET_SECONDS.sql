-- The AUDIT_CARD half of dropping START_OFFSET_SECONDS; see
-- MIG_CARD_DROP_START_OFFSET_SECONDS.sql for why the column is going away.
-- Separate file because the schema runner has no multi-statement support.
ALTER TABLE AUDIT_CARD DROP COLUMN IF EXISTS START_OFFSET_SECONDS;
