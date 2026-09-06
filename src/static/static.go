package static

import "embed"

//go:embed *
var StaticFiles embed.FS

// SQLFiles is the ordered list of game SQL files to execute for database setup,
// run after the framework schema has been applied (the framework owns
// DECK/USER_ACCESS_DECK and deck management; CARD is game-owned and FKs to it).
// Order matters: tables -> migrations -> triggers. Tables must be in dependency
// order. Adding a .sql file without listing it here is a no-op.
var SQLFiles = []string{
	// tables
	"sql/tables/TRACK_TIMELINE_CATEGORY.sql",
	"sql/tables/CARD.sql",
	"sql/tables/AUDIT_CARD.sql",
	"sql/tables/TRACK_TIMELINE_CARD_VIDEO_STATUS.sql",
	"sql/tables/TRACK_TIMELINE_CARD_DUPLICATE_DISMISS.sql",
	"sql/tables/TRACK_TIMELINE_YT_QUOTA_DAY.sql",
	"sql/tables/TRACK_TIMELINE_GAME.sql",
	"sql/tables/TRACK_TIMELINE_ROOM.sql",
	"sql/tables/TRACK_TIMELINE_YEAR_RANGE.sql",
	"sql/tables/TRACK_TIMELINE_DRAW_PILE.sql",
	"sql/tables/TRACK_TIMELINE_CURRENT_CARD.sql",
	"sql/tables/TRACK_TIMELINE_PLAYER_TIMELINE.sql",
	"sql/tables/TRACK_TIMELINE_PLAYER_ORDER.sql",
	"sql/tables/TRACK_TIMELINE_PLAYER_TOKEN.sql",
	"sql/tables/TRACK_TIMELINE_PLACEMENT.sql",
	"sql/tables/TRACK_TIMELINE_TITLE_GUESS.sql",

	// append-only gameplay logs (no FKs by design; they feed the stats pages
	// and must outlive the lobby/game rows, which cascade away on disconnect)
	"sql/tables/TRACK_TIMELINE_LOG_PLACEMENT.sql",
	"sql/tables/TRACK_TIMELINE_LOG_TITLE_GUESS.sql",
	"sql/tables/TRACK_TIMELINE_LOG_CARD.sql",
	"sql/tables/TRACK_TIMELINE_LOG_WIN.sql",
	"sql/tables/TRACK_TIMELINE_LOG_GENRE_ASSIGN.sql",

	// migrations
	"sql/migrations/MIG_TRACK_TIMELINE_TITLE_GUESS_CREATED_ON_DATE_PRECISION.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_TITLE_GUESS_ADD_MATCH_PERCENT.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_TITLE_GUESS_ADD_ARTIST_MATCH_PERCENT.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_LOG_CARD_ADD_BOUGHT_EVENT.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_GAME_CLEAR_LEGACY_CHALLENGE_PHASE.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_GAME_ROUND_PHASE_STEAL_ENUM.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_GAME_ADD_PHASE_STARTED_ON_DATE.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_PLACEMENT_DROP_IS_CHALLENGE.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_PLACEMENT_ADD_EXACT_YEAR_WAGER.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_PLACEMENT_ADD_YEAR_RANGE.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_PLAYER_TIMELINE_DEDUP_GAME_CARD.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_PLAYER_TIMELINE_GAME_CARD_UNIQUE.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_GAME_ADD_STEALER_PLAYER_ID.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_CARD_VIDEO_STATUS_ADD_DURATION_SECONDS.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_GAME_ADD_PLAYBACK_SETTINGS.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_GAME_ADD_GUESS_MODE.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_GAME_ADD_GUESS_MATCH_PERCENT.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_GAME_ADD_GUESS_JUDGE.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_DRAW_PILE_ADD_SHUFFLE_ORDER.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_CARD_VIDEO_STATUS_ADD_AWAITING_VALIDATION.sql",
	"sql/migrations/MIG_TRACK_TIMELINE_CARD_VIDEO_STATUS_ADD_INCORRECT_VIDEO.sql",
	"sql/migrations/MIG_CARD_DROP_START_OFFSET_SECONDS.sql",
	"sql/migrations/MIG_AUDIT_CARD_DROP_START_OFFSET_SECONDS.sql",
	"sql/migrations/MIG_CARD_YOUTUBE_VIDEO_ID_NULLABLE.sql",
	"sql/migrations/MIG_AUDIT_CARD_YOUTUBE_VIDEO_ID_NULLABLE.sql",

	// triggers
	"sql/triggers/TR_AUDIT_CARD_DELETE.sql",
	"sql/triggers/TR_AUDIT_CARD_UPDATE.sql",
	"sql/triggers/TR_SET_CHANGED_ON_DATE_BF_UP_CARD.sql",
}
