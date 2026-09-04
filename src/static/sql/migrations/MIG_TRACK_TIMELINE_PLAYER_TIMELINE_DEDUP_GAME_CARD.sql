-- Remove duplicate (game, card) timeline rows before the unique index is
-- added. Keep the earliest placement. One statement per file — ApplySchema
-- prepares the whole file as a single statement.
DELETE T1
FROM TRACK_TIMELINE_PLAYER_TIMELINE T1
INNER JOIN TRACK_TIMELINE_PLAYER_TIMELINE T2
    ON T1.TRACK_TIMELINE_GAME_ID = T2.TRACK_TIMELINE_GAME_ID
    AND T1.CARD_ID = T2.CARD_ID
    AND (
        T1.PLACED_ON_DATE > T2.PLACED_ON_DATE
        OR (T1.PLACED_ON_DATE = T2.PLACED_ON_DATE AND T1.ID > T2.ID)
    );
