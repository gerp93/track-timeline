package apiTrackTimeline

import (
	"fmt"
	"log"
	"time"

	gsDatabase "github.com/gerp93/gameshell-framework/database"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
)

// turnAbandonedGrace is how long the player on turn can be gone (tab closed,
// connection dropped) before the table stops waiting for them. Long enough to
// ride out a page refresh or a flaky reconnect, short enough that the others are
// not left staring at a dead game.
const turnAbandonedGrace = 30 * time.Second

// turnAbandonedRecheck is how long to wait before looking again when something
// else (an open challenge) is holding the game still at the moment the grace ends.
const turnAbandonedRecheck = 10 * time.Second

// WatchAbandonedTurn is called when a player's last connection drops (the
// framework's OnPlayerInactive). The turn timer is run by the turn player's own
// browser, and every other phase has a server-side timer, so without this a
// player who left on their own turn left the game stuck on them forever: nobody
// else's browser would ever end it.
//
// It only acts in the listening phase; the steal and reveal phases end on their
// own. After turnAbandonedGrace it checks that the same player is still gone and
// the same song is still waiting on them, and if so ends the turn the way a
// timeout would.
func WatchAbandonedTurn(playerId uuid.UUID) {
	player, err := gsDatabase.GetPlayer(playerId)
	if err != nil || player.Id == uuid.Nil {
		return
	}
	game, err := database.GetGame(player.LobbyId)
	if err != nil || game.Id == uuid.Nil || game.GameStatus != database.StatusActive {
		return
	}
	if !game.CurrentPlayerId.Valid || game.CurrentPlayerId.UUID != playerId || game.RoundPhase != database.PhaseListening {
		return
	}
	card, err := database.GetCurrentCard(game.Id)
	if err != nil || card.CardId == uuid.Nil {
		return
	}

	lobbyId, gameId, cardId := player.LobbyId, game.Id, card.CardId
	var recheck func()
	recheck = func() {
		if !AbandonTurnIfStillGone(lobbyId, gameId, playerId, cardId) {
			return
		}
		time.AfterFunc(turnAbandonedRecheck, recheck)
	}
	time.AfterFunc(turnAbandonedGrace, recheck)
}

// AbandonTurnIfStillGone ends the turn if the player is still gone and the song is
// still waiting on them. It returns true only when it should be asked again
// shortly because an open challenge is holding the game.
func AbandonTurnIfStillGone(lobbyId, gameId, playerId, cardId uuid.UUID) bool {
	player, err := gsDatabase.GetPlayer(playerId)
	if err != nil || player.Id == uuid.Nil || player.IsActive {
		return false // back again, or gone for good with the lobby
	}
	game, err := database.GetGameById(gameId)
	if err != nil || game.GameStatus != database.StatusActive || game.RoundPhase != database.PhaseListening {
		return false
	}
	if !game.CurrentPlayerId.Valid || game.CurrentPlayerId.UUID != playerId {
		return false
	}
	if card, err := database.GetCurrentCard(gameId); err != nil || card.CardId != cardId {
		return false // the song was skipped or the turn has moved on
	}
	if open, err := database.GetOpenChallenge(gameId); err == nil && open.Exists() {
		return true
	}

	ctx := gameContext{LobbyId: lobbyId, Game: game, Player: player, UserId: player.UserId}
	log.Printf("turn abandoned by %s in lobby %s; moving on", player.Name, lobbyId)
	discardTurn(ctx, player.Name,
		fmt.Sprintf("<red>%s</> left during their turn", esc(player.Name)),
		player.Name+" left during their turn.")
	return false
}
