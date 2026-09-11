package apiTrackTimeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	gsWebsocket "github.com/gerp93/gameshell-framework/websocket"
	"github.com/google/uuid"

	apiRoom "github.com/gerp93/track-timeline/api/room"
	"github.com/gerp93/track-timeline/database"
)

// This file is the steal mechanic's own orchestration: opening the join
// window, handling a claim, and the server-side scheduled timeouts that make
// both timed phases resolve on their own even if nobody's client ever acts —
// this repo's first server-side timer.
//
// The window opens on every placement, right or wrong, and is never told
// which: revealing that up front would mean a would-be stealer only ever
// gets invited once the original is already known to be wrong, taking no
// real risk. Exactly one player may claim the sole steal attempt (a race,
// first request wins — see database.ClaimSteal); a claim that turns out
// wrong, that lands in the same year range as the original guess, or a
// window/turn that times out, falls back to judging the original placement
// for real (database.ResolveRoundFallbackToOriginal). Matching the original
// range is not a steal even when that slot is also correct for the stealer —
// only a different year window can take the card.
//
// Every scheduled timeout re-validates the game is still in exactly the
// phase/instant it was scheduled for (phaseStillCurrent) before doing
// anything: a real player action in the meantime advances the phase and
// stamps a new PHASE_STARTED_ON_DATE, which makes the stale timer's check
// fail and turns it into a no-op instead of double-resolving the round.

// stealJoinPayload is the steal: websocket message, broadcast once when the
// turn player's placement is committed. Every client renders the same
// countdown computed from DeadlineMs, rather than each starting its own
// local timer on receipt. The year bounds are the implied slot on the
// placer's timeline — including for exact-year lock-ins — so the digits of
// an exact-year wager are never leaked here.
type stealJoinPayload struct {
	DeadlineMs   int64 `json:"deadlineMs"`
	HasLowerYear bool  `json:"hasLowerYear,omitempty"`
	LowerYear    int   `json:"lowerYear,omitempty"`
	HasUpperYear bool  `json:"hasUpperYear,omitempty"`
	UpperYear    int   `json:"upperYear,omitempty"`
}

func sendStealJoin(lobbyId uuid.UUID, deadline time.Time, timeline []database.TimelineCard, position int) {
	payload := stealJoinPayload{DeadlineMs: deadline.UnixMilli()}
	if position > 0 {
		payload.HasLowerYear = true
		payload.LowerYear = timeline[position-1].ReleaseYear
	}
	if position < len(timeline) {
		payload.HasUpperYear = true
		payload.UpperYear = timeline[position].ReleaseYear
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		log.Println(err)
		return
	}
	gsWebsocket.LobbyBroadcast(lobbyId, "steal:"+string(encoded))
	apiRoom.MirrorBroadcast(lobbyId, "steal:"+string(encoded))
}

// stealTurnPayload is the stealTurn: websocket message, broadcast once the
// sole steal attempt has been claimed.
type stealTurnPayload struct {
	DeadlineMs  int64  `json:"deadlineMs"`
	StealerId   string `json:"stealerId"`
	StealerName string `json:"stealerName"`
}

func sendStealTurn(lobbyId uuid.UUID, deadline time.Time, stealerId uuid.UUID, stealerName string) {
	payload := stealTurnPayload{
		DeadlineMs:  deadline.UnixMilli(),
		StealerId:   stealerId.String(),
		StealerName: stealerName,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		log.Println(err)
		return
	}
	gsWebsocket.LobbyBroadcast(lobbyId, "stealTurn:"+string(encoded))
	apiRoom.MirrorBroadcast(lobbyId, "stealTurn:"+string(encoded))
}

// phaseStillCurrent reports whether the game is still in exactly the phase a
// timer was scheduled for, at the same PHASE_STARTED_ON_DATE instant. Both
// the broadcast deadline and this check are always derived from the same
// database read (never from a Go-side time.Now() computed independently),
// since the database's clock and this process's clock are not the same
// clock and would never compare equal.
func phaseStillCurrent(gameId uuid.UUID, expectedPhase string, expectedStartedAt time.Time) bool {
	game, err := database.GetGameById(gameId)
	if err != nil {
		return false
	}
	if game.RoundPhase != expectedPhase {
		return false
	}
	if !game.PhaseStartedOnDate.Valid {
		return false
	}
	return game.PhaseStartedOnDate.Time.Equal(expectedStartedAt)
}

// openStealJoin transitions to the steal-join phase, broadcasts it with a
// server-computed deadline, and schedules the fallback that fires if nobody
// claims the steal attempt in time.
func openStealJoin(ctx gameContext, timeline []database.TimelineCard, position int) {
	if err := database.SetRoundPhase(ctx.Game.Id, database.PhaseStealJoin); err != nil {
		log.Println(err)
		sendStatus(ctx.LobbyId, "Something went wrong opening the steal window.")
		return
	}

	game, err := database.GetGameById(ctx.Game.Id)
	if err != nil || !game.PhaseStartedOnDate.Valid {
		log.Println("failed to read back steal-join start:", err)
		sendStatus(ctx.LobbyId, "Something went wrong opening the steal window.")
		return
	}
	startedAt := game.PhaseStartedOnDate.Time
	deadline := startedAt.Add(database.StealJoinWindow)

	sendStealJoin(ctx.LobbyId, deadline, timeline, position)
	// Status only — the definitive "placed correctly / stole / nobody" line
	// lands at reveal so chat is not "locked in" then "placed correctly".
	sendStatus(ctx.LobbyId, fmt.Sprintf("%s locked in — steal window open", ctx.Player.Name))
	refresh(ctx.LobbyId)

	lobbyId, gameId := ctx.LobbyId, ctx.Game.Id
	time.AfterFunc(time.Until(deadline), func() {
		if !phaseStillCurrent(gameId, database.PhaseStealJoin, startedAt) {
			return
		}
		resolveWithFallback(lobbyId, gameId)
	})
}

// beginStealTurnBroadcast tells every client the steal has been claimed —
// database.ClaimSteal has already spent the claimant's token and moved the
// phase to PhaseStealTurn by the time this runs — and schedules this specific
// turn's own timeout.
func beginStealTurnBroadcast(lobbyId uuid.UUID, gameId uuid.UUID, stealerId uuid.UUID, stealerName string) {
	game, err := database.GetGameById(gameId)
	if err != nil || !game.PhaseStartedOnDate.Valid {
		log.Println("failed to read back steal-turn start:", err)
		return
	}
	startedAt := game.PhaseStartedOnDate.Time
	deadline := startedAt.Add(database.StealTurnWindow)

	sendStealTurn(lobbyId, deadline, stealerId, stealerName)
	refresh(lobbyId)

	time.AfterFunc(time.Until(deadline), func() {
		if !phaseStillCurrent(gameId, database.PhaseStealTurn, startedAt) {
			return
		}
		announce(lobbyId, "<red>Time's up</> — the steal attempt failed")
		resolveWithFallback(lobbyId, gameId)
	})
}

// resolveWithFallback re-judges the turn player's own original placement
// (nobody claimed the steal, or a claimed attempt missed/timed out) and
// resolves the round accordingly.
func resolveWithFallback(lobbyId uuid.UUID, gameId uuid.UUID) {
	game, err := database.GetGameById(gameId)
	if err != nil {
		log.Println(err)
		return
	}
	ctx := gameContext{LobbyId: lobbyId, Game: game}

	outcome, err := database.ResolveRoundFallbackToOriginal(gameId)
	if err != nil {
		if errors.Is(err, database.ErrRoundAlreadyResolved) {
			return
		}
		log.Println(err)
		return
	}
	announceAndFinish(ctx, outcome)
}
