package game

import (
	"github.com/gerp93/track-timeline/database"
	"github.com/google/uuid"
)

// TrackTimeline implements gameshell.Game. Most hooks are deliberate no-ops:
// per-room and per-player game state is created lazily by the API layer the
// first time a lobby is visited, and it cascades away with the framework's
// LOBBY row when the last websocket client disconnects.
type TrackTimeline struct{}

func (TrackTimeline) OnRoomCreated(lobbyId uuid.UUID) error { return nil }

func (TrackTimeline) OnPlayerJoined(playerId uuid.UUID) error { return nil }

func (TrackTimeline) OnPlayerActive(playerId uuid.UUID) error { return nil }

func (TrackTimeline) OnPlayerInactive(playerId uuid.UUID) error { return nil }

func (TrackTimeline) OnRoomEmpty(lobbyId uuid.UUID) error { return nil }

// OnDeckDeleting audits this game's own cards before the framework deletes the
// DECK row. MariaDB's ON DELETE CASCADE from DECK to CARD does not fire CARD's
// own triggers, so without this hook the cards would vanish unaudited. Any
// future game-owned table that FKs to DECK belongs here too — never as a
// trigger on the framework's DECK table.
func (TrackTimeline) OnDeckDeleting(deckId uuid.UUID) error {
	return database.AuditDeckCardsAsDeleted(deckId)
}
