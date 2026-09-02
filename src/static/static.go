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
	"sql/tables/TRACK_TIMELINE_GAME.sql",
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

	// triggers
	"sql/triggers/TR_AUDIT_CARD_DELETE.sql",
	"sql/triggers/TR_AUDIT_CARD_UPDATE.sql",
	"sql/triggers/TR_SET_CHANGED_ON_DATE_BF_UP_CARD.sql",
}
