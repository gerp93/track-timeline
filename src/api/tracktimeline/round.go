package apiTrackTimeline

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	gsDatabase "github.com/gerp93/gameshell-framework/database"
	gsWebsocket "github.com/gerp93/gameshell-framework/websocket"
	"github.com/google/uuid"

	apiRoom "github.com/gerp93/track-timeline/api/room"
	"github.com/gerp93/track-timeline/database"
)

// PlaceCard records the placement of the player on turn — either a chosen
// position, or (if the year field is set) an exact-year guess that the server
// converts into the implied position. Exact-year attempts require wagering at
// least one token: spent up front, doubled back on a correct year (net +wager),
// kept by the bank on a miss. Landing in the right timeline range still wins
// the card even when the year digits are wrong — the wager only buys the
// bonus. Title/artist guessing rides along free when the lobby's guess mode
// allows it.
//
// The placement is judged immediately: correct wins the card outright (subject
// to steal), wrong opens the steal-join window if anyone is eligible, or
// resolves the round with nobody taking the card if nobody is.
func PlaceCard(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if challengeInProgress(w, ctx) {
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	if ctx.Game.GameStatus != database.StatusActive {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The game is not running."))
		return
	}
	if ctx.Game.RoundPhase != database.PhaseListening {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("It is too late to place this song."))
		return
	}
	if !ctx.Game.CurrentPlayerId.Valid || ctx.Game.CurrentPlayerId.UUID != ctx.Player.Id {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("It is not your turn."))
		return
	}

	// The answer is read server-side here (never sent to the client) both to
	// judge the placement itself and for judging whatever guess came in with
	// this same submission.
	card, err := database.GetCurrentCardAnswer(ctx.Game.Id)
	if err != nil || card.CardId == uuid.Nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("No song is in play."))
		return
	}

	timeline, err := database.GetPlayerTimeline(ctx.Game.Id, ctx.Player.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to read your timeline."))
		return
	}

	var position int
	var exactYearGuess int
	var yearWager int
	usedExactYear := strings.TrimSpace(r.FormValue("year")) != ""
	if usedExactYear {
		yearRaw := strings.TrimSpace(r.FormValue("year"))
		if len(yearRaw) != 4 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Exact year must be a 4-digit year."))
			return
		}
		exactYearGuess, err = strconv.Atoi(yearRaw)
		if err != nil || exactYearGuess < 1000 || exactYearGuess > 9999 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Exact year must be a 4-digit year."))
			return
		}

		yearWager, err = strconv.Atoi(strings.TrimSpace(r.FormValue("yearWager")))
		if err != nil || yearWager < 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Wager at least 1 token to attempt an exact-year guess."))
			return
		}
		tokens, tokErr := database.GetPlayerTokens(ctx.Game.Id, ctx.Player.Id)
		if tokErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to check your tokens."))
			return
		}
		if yearWager > tokens {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("You do not have enough tokens for that wager."))
			return
		}

		position = database.PositionForYear(timeline, exactYearGuess)
	} else {
		position, err = strconv.Atoi(r.FormValue("position"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Invalid position."))
			return
		}
		if position < 0 || position > len(timeline) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("That position is not on your timeline."))
			return
		}
	}

	exactYearNull := sql.NullInt64{}
	if usedExactYear {
		exactYearNull = sql.NullInt64{Int64: int64(exactYearGuess), Valid: true}
	}
	yearRange := database.PlacementYearRangeOf(timeline, position)
	if err := database.CommitPlacement(ctx.Game.Id, ctx.Player.Id, position, exactYearNull, yearWager, yearRange); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("You have already placed this song."))
		return
	}

	// Room mode publishes the placement on the TV as soon as it locks in —
	// the whole couch is watching one board, so hiding the slot would just
	// look like the phone ate the tap. Remote lobbies still keep it private
	// until reveal so stealers cannot copy the slot.
	if isRoom, roomErr := database.LobbyIsRoom(ctx.LobbyId); roomErr == nil && isRoom {
		if usedExactYear {
			announce(ctx.LobbyId, fmt.Sprintf(
				"<blue>%s</> locked in exact year <blue>%d</>",
				esc(ctx.Player.Name), exactYearGuess,
			))
		} else {
			announce(ctx.LobbyId, fmt.Sprintf(
				"<blue>%s</> placed in <blue>%s</>",
				esc(ctx.Player.Name), esc(yearRange.Format()),
			))
		}
	}

	// Exact-year wager settle is private to the placer. Digits right or wrong
	// does not change whether steal opens — that is decided solely by
	// AnyEligibleToSteal below, same as an ordinary slot lock-in. Lobby chat
	// for the wager waits until reveal (see announceAndFinish).
	exactYearCorrect := usedExactYear && exactYearGuess == card.ReleaseYear
	if usedExactYear {
		if _, err := database.AddPlayerTokens(ctx.Game.Id, ctx.Player.Id, -yearWager); err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to spend your wager."))
			return
		}
		// The stake is public the moment it is put down; the year digits and
		// whether they were right wait for the reveal (see announceAndFinish).
		announce(ctx.LobbyId, fmt.Sprintf("<blue>%s</> wagered %s on an exact year",
			esc(ctx.Player.Name), tokenCount(yearWager)))
		if exactYearCorrect {
			// Double-or-nothing: return 2× the wager for a net gain of the wager.
			if _, err := database.AddPlayerTokens(ctx.Game.Id, ctx.Player.Id, 2*yearWager); err != nil {
				log.Println(err)
			}
		}
	}

	// Whatever is in the guess boxes when the placement locks in is this
	// player's guess. Already-locked is fine: they pressed Guess first.
	title, artist, _ := guessFields(r)
	lockGuess(ctx, card.CardId, title, artist)

	// The song stops the moment a placement is locked in: leaving it playing
	// through a steal window would hand stealers more listening time than the
	// player on turn got.
	gsWebsocket.LobbyBroadcast(ctx.LobbyId, "songStop")
	apiRoom.MirrorBroadcast(ctx.LobbyId, "songStop")

	// Judged and logged for stats immediately, but deliberately not acted on
	// or revealed yet if anyone could steal: see the steal.go doc comment for
	// why. A correct exact-year (or correct slot) still opens steal whenever
	// anyone else is eligible — correctness must not be inferable from whether
	// a window appears. If literally nobody could steal, resolve outright.
	correct := database.IsPlacementCorrect(timeline, position, card.ReleaseYear)
	if logErr := database.LogPlacement(ctx.UserId, card.CardId, card.ReleaseYear, false, correct); logErr != nil {
		log.Println(logErr)
	}

	eligible, err := database.AnyEligibleToSteal(ctx.Game.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check for stealers."))
		return
	}

	if usedExactYear {
		if exactYearCorrect {
			msg := fmt.Sprintf("alert:Exact year %d — %s.", exactYearGuess, tokensWonLost(yearWager))
			if eligible {
				msg = fmt.Sprintf("alert:Exact year %d — %s. Steal window still open for everyone else.",
					exactYearGuess, tokensWonLost(yearWager))
			}
			gsWebsocket.PlayerBroadcast(ctx.Player.Id, msg)
			apiRoom.MirrorPlayerBroadcast(ctx.LobbyId, ctx.Player.Id, msg)
		} else {
			msg := fmt.Sprintf("alert:Guessed %d, was %d — %s.",
				exactYearGuess, card.ReleaseYear, tokensWonLost(-yearWager))
			if eligible {
				msg = fmt.Sprintf("alert:Guessed %d, was %d — %s. Steal window still open for everyone else.",
					exactYearGuess, card.ReleaseYear, tokensWonLost(-yearWager))
			}
			gsWebsocket.PlayerBroadcast(ctx.Player.Id, msg)
			apiRoom.MirrorPlayerBroadcast(ctx.LobbyId, ctx.Player.Id, msg)
		}
	}

	if !eligible {
		var outcome database.RoundOutcome
		if correct {
			outcome, err = database.ResolveRoundWon(ctx.Game.Id, ctx.Player.Id, ctx.Player.Name, position, false)
		} else {
			outcome, err = database.ResolveRoundFallbackToOriginal(ctx.Game.Id)
		}
		if err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to resolve the round."))
			return
		}
		announceAndFinish(ctx, outcome)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Placed."))
		return
	}

	openStealJoin(ctx, timeline, position)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Placed."))
}

// BuyCard spends database.BuyCardCost tokens to draw a fresh song straight
// from the top of the draw pile and place it, correctly, directly onto the
// buyer's own timeline — no listen, no guess, and no interruption to
// whatever round is already in progress. Available to any player, not just
// whoever is on turn, at any time except while the UI is blocked by a timer
// (steal_join, steal_turn) or already showing the reveal — there is nothing
// left to buy once the answer for this round is public.
func BuyCard(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if challengeInProgress(w, ctx) {
		return
	}

	if ctx.Game.GameStatus != database.StatusActive {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The game is not running."))
		return
	}
	if ctx.Game.RoundPhase != database.PhaseListening {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("You can't buy a card right now."))
		return
	}

	bought, err := database.BuyCard(ctx.Game.Id, ctx.Player.Id)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(capitalize(err.Error()) + "."))
		return
	}

	announce(ctx.LobbyId, fmt.Sprintf(
		"<blue>%s</> %s to buy “%s” by %s (%d)",
		esc(ctx.Player.Name), tokensSpent(database.BuyCardCost), esc(bought.Title), esc(bought.Artist), bought.ReleaseYear,
	))
	refresh(ctx.LobbyId)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Bought."))
}

// ClaimSteal attempts to claim the sole steal attempt during the join window
// that opens on every placement. First request wins the race (see
// database.ClaimSteal); everyone else who clicks after gets told someone
// already claimed it. A successful claim immediately spends the claimant's
// token — there is no separate "join for free, pay later" step now that
// there is only one attempt, not a queue.
func ClaimSteal(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if challengeInProgress(w, ctx) {
		return
	}

	if ctx.Game.RoundPhase != database.PhaseStealJoin {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("There is no steal window open."))
		return
	}
	if ctx.Game.CurrentPlayerId.Valid && ctx.Game.CurrentPlayerId.UUID == ctx.Player.Id {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("You cannot steal your own placement."))
		return
	}

	tokens, err := database.GetPlayerTokens(ctx.Game.Id, ctx.Player.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check your tokens."))
		return
	}
	if tokens < database.StealCost {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("You need " + tokenCount(database.StealCost) + " to steal."))
		return
	}

	claimed, err := database.ClaimSteal(ctx.Game.Id, ctx.Player.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to claim the steal."))
		return
	}
	if !claimed {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Someone already claimed it."))
		return
	}

	announce(ctx.LobbyId, fmt.Sprintf("<blue>%s</> %s to steal", esc(ctx.Player.Name), tokensSpent(database.StealCost)))
	beginStealTurnBroadcast(ctx.LobbyId, ctx.Game.Id, ctx.Player.Id, ctx.Player.Name)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Claimed."))
}

// AttemptSteal is the claimant's own placement attempt, during their
// steal_turn window. Wrong (or the window timing out, handled separately by
// the scheduled server-side timeout) falls back to judging the turn player's
// original placement for real — there is no second claimant.
func AttemptSteal(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if challengeInProgress(w, ctx) {
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	if ctx.Game.RoundPhase != database.PhaseStealTurn {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("It is not a steal turn."))
		return
	}
	if !ctx.Game.StealerPlayerId.Valid || ctx.Game.StealerPlayerId.UUID != ctx.Player.Id {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("It is not your turn to steal."))
		return
	}

	position, err := strconv.Atoi(r.FormValue("position"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid position."))
		return
	}

	timeline, err := database.GetPlayerTimeline(ctx.Game.Id, ctx.Player.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to read your timeline."))
		return
	}
	if position < 0 || position > len(timeline) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("That position is not on your timeline."))
		return
	}

	card, err := database.GetCurrentCardAnswer(ctx.Game.Id)
	if err != nil || card.CardId == uuid.Nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("No song is in play."))
		return
	}

	correct := database.IsPlacementCorrect(timeline, position, card.ReleaseYear)
	if logErr := database.LogPlacement(ctx.UserId, card.CardId, card.ReleaseYear, true, correct); logErr != nil {
		log.Println(logErr)
	}

	// If the stealer and the original are both correct for the year, that is
	// not a steal — the card stays with the player on turn. Only a correct
	// steal against a wrong original takes the card. Use the range
	// snapshotted at lock-in so a buy mid-window cannot rewrite whether the
	// original was right. If we cannot read the placement, fail closed.
	if correct {
		placement, err := database.GetPlacement(ctx.Game.Id)
		if err != nil || placement.Id == uuid.Nil {
			log.Println("steal both-valid check: missing placement:", err)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to read the original placement."))
			return
		}
		originalRange := placement.YearRange
		stealerRange := database.PlacementYearRangeOf(timeline, position)

		if originalRange.Contains(card.ReleaseYear) {
			announce(ctx.LobbyId, fmt.Sprintf(
				"<red>%s</>'s steal matched a valid range too (%s vs %s's %s) — card stays with %s. Year was %d.",
				esc(ctx.Player.Name),
				esc(stealerRange.Format()),
				esc(placement.PlayerName),
				esc(originalRange.Format()),
				esc(placement.PlayerName),
				card.ReleaseYear,
			))
			outcome, err := database.ResolveRoundFallbackToOriginal(ctx.Game.Id)
			if err != nil {
				if errors.Is(err, database.ErrRoundAlreadyResolved) {
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte("Round already resolved."))
					return
				}
				log.Println(err)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("Failed to resolve the round."))
				return
			}
			announceAndFinish(ctx, outcome)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Both placements were valid — card stays with the original."))
			return
		}

		outcome, err := database.ResolveRoundWon(ctx.Game.Id, ctx.Player.Id, ctx.Player.Name, position, true)
		if err != nil {
			if errors.Is(err, database.ErrRoundAlreadyResolved) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("Round already resolved."))
				return
			}
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to resolve the round."))
			return
		}
		outcome.OriginalRangeLabel = originalRange.Format()
		outcome.StealerRangeLabel = stealerRange.Format()
		outcome.OriginalPlayerName = placement.PlayerName
		announceAndFinish(ctx, outcome)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Stolen."))
		return
	}

	announce(ctx.LobbyId, fmt.Sprintf("<red>%s</>'s steal attempt missed", esc(ctx.Player.Name)))
	outcome, err := database.ResolveRoundFallbackToOriginal(ctx.Game.Id)
	if err != nil {
		if errors.Is(err, database.ErrRoundAlreadyResolved) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Round already resolved."))
			return
		}
		log.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to resolve the round."))
		return
	}
	announceAndFinish(ctx, outcome)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Wrong."))
}

// announceAndFinish builds the reveal payload and chat lines from a round
// outcome (however it was reached — a correct placement, a steal, a buy, or
// nobody getting it) and hands off to finishRound to check for a game winner
// and either end the game or advance the turn.
func announceAndFinish(ctx gameContext, outcome database.RoundOutcome) {
	payload := resultPayload{
		Title:          outcome.Title,
		Artist:         outcome.Artist,
		ReleaseYear:    outcome.ReleaseYear,
		WinnerName:     outcome.WinnerName,
		WonByChallenge: outcome.WonByChallenge,

		TurnPlayerName:    outcome.CurrentPlayerName,
		TurnPlayerCorrect: outcome.CurrentPlayerCorrect,
		TurnPlayerRange:   outcome.CurrentPlayerRange,
	}
	if outcome.HasExactYearGuess {
		payload.TurnPlayerExactYear = outcome.ExactYearGuess
	}

	// Exact-year wager chat was deferred from PlaceCard so stealers never saw
	// the year digits (or whether the digits were right) before acting.
	if outcome.HasExactYearGuess {
		if outcome.ExactYearCorrect {
			announce(ctx.LobbyId, fmt.Sprintf(
				"<green>%s</> nailed the exact year %d — %s",
				esc(outcome.ExactYearPlayer), outcome.ExactYearGuess, wagerResult(outcome.YearWager, true),
			))
		} else {
			announce(ctx.LobbyId, fmt.Sprintf(
				"<red>%s</> missed the exact year (guessed %d, was %d) — %s",
				esc(outcome.ExactYearPlayer), outcome.ExactYearGuess, outcome.ReleaseYear, wagerResult(outcome.YearWager, false),
			))
		}
	}

	// Every guess made this round, across every song of it, judged together just
	// before the reveal -- held back from chat until now so nobody could read off
	// who was right about the title/artist before the song was actually
	// revealed.
	announceGuesses(ctx.LobbyId, outcome.Guesses)

	// Every right part of a guess earns its own tokens -- there is no
	// single-token race, so each winner gets their own announcement and popup
	// line, with what they earned.
	for _, winner := range outcome.GuessTokenWinners {
		earned := database.GuessTokensEarned(winner)
		payload.GuessTokenWinners = append(payload.GuessTokenWinners, guessTokenWinnerPayload{
			Name:      winner.PlayerName,
			GuessText: winner.GuessText,
			Tokens:    earned,
		})
		announce(ctx.LobbyId, fmt.Sprintf(
			"<green>%s</> earned %s for their guess",
			esc(winner.PlayerName), tokenCount(earned),
		))
	}

	songLine := fmt.Sprintf("%s — “%s” (%d)", esc(outcome.Artist), esc(outcome.Title), outcome.ReleaseYear)

	switch {
	case outcome.WinnerPlayerId.Valid && outcome.WonByChallenge:
		payload.Type = "won"
		if outcome.OriginalRangeLabel != "" && outcome.StealerRangeLabel != "" {
			payload.BottomMessage = fmt.Sprintf(
				"%s stole it! %s — “%s” (%d). %s: %s · %s: %s",
				outcome.WinnerName, outcome.Artist, outcome.Title, outcome.ReleaseYear,
				outcome.OriginalPlayerName, outcome.OriginalRangeLabel,
				outcome.WinnerName, outcome.StealerRangeLabel,
			)
			announce(ctx.LobbyId, fmt.Sprintf(
				"<green>%s</> stole the card — %s — “%s”. %s's range: %s; %s's range: %s; year was %d.",
				esc(outcome.WinnerName),
				esc(outcome.Artist),
				esc(outcome.Title),
				esc(outcome.OriginalPlayerName),
				esc(outcome.OriginalRangeLabel),
				esc(outcome.WinnerName),
				esc(outcome.StealerRangeLabel),
				outcome.ReleaseYear,
			))
		} else {
			payload.BottomMessage = fmt.Sprintf("%s stole it! %s — “%s” (%d)",
				outcome.WinnerName, outcome.Artist, outcome.Title, outcome.ReleaseYear)
			announce(ctx.LobbyId, fmt.Sprintf("<green>%s</> stole the card — %s", esc(outcome.WinnerName), songLine))
		}
		attachWinCelebration(&payload, ctx.Game.Id, outcome.WinnerPlayerId.UUID)
	case outcome.WinnerPlayerId.Valid:
		payload.Type = "won"
		payload.BottomMessage = fmt.Sprintf("%s placed it correctly. %s — “%s” (%d)",
			outcome.WinnerName, outcome.Artist, outcome.Title, outcome.ReleaseYear)
		announce(ctx.LobbyId, fmt.Sprintf("<green>%s</> placed it correctly — %s", esc(outcome.WinnerName), songLine))
		attachWinCelebration(&payload, ctx.Game.Id, outcome.WinnerPlayerId.UUID)
	default:
		payload.Type = "discarded"
		payload.BottomMessage = fmt.Sprintf("Nobody got it. %s — “%s” (%d)",
			outcome.Artist, outcome.Title, outcome.ReleaseYear)
		announce(ctx.LobbyId, fmt.Sprintf("<red>Nobody placed it correctly</> — %s", songLine))
		// Commiseration belongs to the turn player who missed — same role as
		// timeline-trivia's incorrect-guess celebration, not the "everyone
		// missed / revealed" case that carries none.
		if outcome.CurrentPlayerId.Valid {
			attachLoseCelebration(&payload, ctx.Game.Id, outcome.CurrentPlayerId.UUID)
		}
	}

	finishRound(ctx, payload)
}

// attachWinCelebration stamps the winner's account-page celebration onto the
// reveal payload so every client can render their GIF/message.
func attachWinCelebration(payload *resultPayload, gameId uuid.UUID, playerId uuid.UUID) {
	userId := userIdForPlayer(gameId, playerId)
	if userId == uuid.Nil {
		return
	}
	payload.UserId = userId.String()
	payload.Celebration, payload.HasGif = winCelebrationFor(userId)
}

// attachLoseCelebration stamps the turn player's loss-commiseration onto a
// discarded-round payload.
func attachLoseCelebration(payload *resultPayload, gameId uuid.UUID, playerId uuid.UUID) {
	userId := userIdForPlayer(gameId, playerId)
	if userId == uuid.Nil {
		return
	}
	payload.UserId = userId.String()
	payload.Celebration, payload.HasGif = loseCelebrationFor(userId)
}

// finishRound is the tail both a judged round (resolveAndAnnounce) and a
// bought card (BuyCard) share once they have an outcome: check for a game
// winner and either end the game or advance the turn, then send the result.
func finishRound(ctx gameContext, payload resultPayload) {
	// Marks the end of this turn's chat cluster (guesses, tokens earned, the
	// placement/steal verdict, and whichever of this function's own
	// announcements below) regardless of which of the three exit paths below
	// is taken -- a defer runs once no matter which return fires.
	defer announceDivider(ctx.LobbyId)

	winnerUserId, err := database.CheckWinner(ctx.Game.Id)
	if err != nil {
		log.Println(err)
	}

	if winnerUserId != uuid.Nil {
		payload.GameOver = true
		payload.Type = "won"
		payload.UserId = winnerUserId.String()
		if user, err := gsDatabase.GetUser(winnerUserId); err == nil {
			payload.WinnerName = user.Name
			payload.BottomMessage = fmt.Sprintf("%s wins! %s — “%s” (%d)",
				user.Name, payload.Artist, payload.Title, payload.ReleaseYear)
			announce(ctx.LobbyId, fmt.Sprintf("<green>%s wins the game!</>", esc(user.Name)))
		}
		// Prefer the game winner's celebration over any round-level stamp —
		// they are usually the same person, but a steal-into-win should show
		// the stealer who just hit the cards-to-win threshold.
		payload.Celebration, payload.HasGif = winCelebrationFor(winnerUserId)
		payload.WinVideoId, payload.WinVideoStartSeconds = winVideoFor(winnerUserId)
		sendResult(ctx.LobbyId, payload)
		gsWebsocket.LobbyBroadcast(ctx.LobbyId, "reload")
		apiRoom.MirrorBroadcast(ctx.LobbyId, "reload")
		return
	}

	if err := database.AdvanceToNextPlayer(ctx.Game.Id); err != nil {
		log.Println(err)
		// A dry draw pile is the expected way to get here.
		payload.BottomMessage += " No songs left — the game is over."
		sendResult(ctx.LobbyId, payload)
		announce(ctx.LobbyId, "<red>The draw pile is empty</> — no more songs to play")
		gsWebsocket.LobbyBroadcast(ctx.LobbyId, "reload")
		apiRoom.MirrorBroadcast(ctx.LobbyId, "reload")
		return
	}

	payload.NextPlayerName = currentPlayerName(ctx.Game.Id)
	sendResult(ctx.LobbyId, payload)
	refresh(ctx.LobbyId)
}

// guessRequest is a guess (or a draft of one) that has passed the checks they
// share: the game is running, the song is not yet revealed, the player is allowed
// to guess, and the song the guess was typed for is part of this round.
type guessRequest struct {
	ctx    gameContext
	cardId uuid.UUID
	title  string
	artist string
}

// readGuessRequest reads and validates a guess or a draft, writing its own error
// response and returning false on any problem.
//
// The form is stamped with the song it was typed for (cardId), and that, not
// whichever song happens to be in play when the request lands, is what the guess
// is for. Otherwise a guess typed for a song that was skipped a moment before the
// request arrived would be filed against the next one. A song skipped earlier in
// this round still counts; one from an earlier round, or a made-up id, does not.
// A client that sends no cardId is taken to mean the song in play.
func readGuessRequest(w http.ResponseWriter, r *http.Request) (guessRequest, bool) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return guessRequest{}, false
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return guessRequest{}, false
	}

	if ctx.Game.GameStatus != database.StatusActive {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The game is not running."))
		return guessRequest{}, false
	}
	if ctx.Game.RoundPhase == database.PhaseReveal {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The song has already been revealed."))
		return guessRequest{}, false
	}

	// Room mode: only the player on turn may guess. Remote lobbies keep the
	// free-for-all race; the TV already shows one shared board, so letting
	// every phone guess would drown the night in parallel attempts.
	if isRoom, roomErr := database.LobbyIsRoom(ctx.LobbyId); roomErr == nil && isRoom {
		if !ctx.Game.CurrentPlayerId.Valid || ctx.Game.CurrentPlayerId.UUID != ctx.Player.Id {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Only the player on turn can guess in room mode."))
			return guessRequest{}, false
		}
	}

	card, err := database.GetCurrentCard(ctx.Game.Id)
	if err != nil || card.CardId == uuid.Nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("No song is in play."))
		return guessRequest{}, false
	}
	cardId := card.CardId
	if said := strings.TrimSpace(r.FormValue("cardId")); said != "" {
		parsed, parseErr := uuid.Parse(said)
		if parseErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Invalid song."))
			return guessRequest{}, false
		}
		cardId = parsed
	}
	inRound, err := database.IsRoundCard(ctx.Game.Id, cardId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check the song."))
		return guessRequest{}, false
	}
	if !inRound {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("That song is already over, so your guess was not used."))
		return guessRequest{}, false
	}

	title, artist, _ := guessFields(r)
	return guessRequest{ctx: ctx, cardId: cardId, title: title, artist: artist}, true
}

// SubmitGuess locks in a player's free-form artist/title guess when they press
// Guess. Nothing is judged here: the guess is stored against the song it was
// typed for and judged, with every other guess of the round, when the round
// resolves (see database.JudgeRoundGuesses), which is also when its tokens are
// paid. The turn player's guess is bundled into PlaceCard instead. A guess costs
// nothing to attempt regardless of outcome.
func SubmitGuess(w http.ResponseWriter, r *http.Request) {
	req, ok := readGuessRequest(w, r)
	if !ok {
		return
	}
	ctx := req.ctx
	if challengeInProgress(w, ctx) {
		return
	}

	if req.title == "" && req.artist == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Type a guess first."))
		return
	}

	if err := lockGuess(ctx, req.cardId, req.title, req.artist); err != nil {
		if errors.Is(err, database.ErrGuessLocked) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("You have already guessed this song."))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to record your guess."))
		return
	}

	// The guess was typed for a song that has since been skipped: it still
	// counts, against that song, so say so rather than leaving the player to
	// wonder where it went.
	message := "Guess recorded."
	private := "Guess locked in — the Quizmaster judges it when the round ends."
	if current, err := database.GetCurrentCard(ctx.Game.Id); err == nil && current.CardId != req.cardId {
		message = "Guess recorded for the song that was just skipped."
		private = "Your guess counts for the song that was just skipped — the Quizmaster judges it when the round ends."
	}
	gsWebsocket.PlayerBroadcast(ctx.Player.Id, "alert:"+private)
	apiRoom.MirrorPlayerBroadcast(ctx.LobbyId, ctx.Player.Id, "alert:"+private)

	refresh(ctx.LobbyId)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(message))
}

// SaveGuessDraft stores whatever a player has typed so far as a draft guess about
// the song it was typed for. The browser calls it as they type, so that a song
// skipped, or a placement locked in, before they pressed Guess still credits what
// was in the boxes: the draft is judged with the round like any other guess. A
// locked guess is never changed by it.
func SaveGuessDraft(w http.ResponseWriter, r *http.Request) {
	req, ok := readGuessRequest(w, r)
	if !ok {
		return
	}

	if err := database.SaveGuess(req.ctx.Game.Id, req.ctx.Player.Id, req.cardId, req.title, req.artist, false); err != nil {
		log.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to save your guess."))
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Saved."))
}

// lockGuess stores a player's guess about a song and locks it, returning
// database.ErrGuessLocked if they had already locked one. Nothing is judged; see
// database.JudgeRoundGuesses. An empty guess is a no-op.
func lockGuess(ctx gameContext, cardId uuid.UUID, title, artist string) error {
	if title == "" && artist == "" {
		return nil
	}
	if err := database.SaveGuess(ctx.Game.Id, ctx.Player.Id, cardId, title, artist, true); err != nil {
		if !errors.Is(err, database.ErrGuessLocked) {
			log.Println(err)
		}
		return err
	}

	// Room TV log: someone locked a guess -- never the words or the verdict.
	if isRoom, roomErr := database.LobbyIsRoom(ctx.LobbyId); roomErr == nil && isRoom {
		announce(ctx.LobbyId, fmt.Sprintf("<blue>%s</> locked a guess", esc(ctx.Player.Name)))
		refresh(ctx.LobbyId)
	}
	return nil
}

// guessFields reads the split song-name and artist boxes. Each box is capped at
// the same 250 characters the form enforces, since a hand-built request skips the
// form and everything typed here is sent on to the AI judge.
func guessFields(r *http.Request) (title, artist, combined string) {
	title = truncateRunes(strings.TrimSpace(r.FormValue("guessTitle")), 250)
	artist = truncateRunes(strings.TrimSpace(r.FormValue("guessArtist")), 250)
	return title, artist, database.CombineGuess(title, artist)
}

// describePendingGuess is what a player who has locked in a guess sees in place
// of the guess form until the round ends and it is judged. Returns ok=false if
// they have no locked guess about the song in play.
func describePendingGuess(gameId uuid.UUID, playerId uuid.UUID) (string, bool) {
	mine, found, err := database.GetPlayerGuess(gameId, playerId)
	if err != nil {
		log.Println(err)
		return "", false
	}
	if !found || !mine.Locked {
		return "", false
	}
	return fmt.Sprintf("You guessed \u201c%s\u201d. The Quizmaster judges it when the round ends.", mine.GuessText), true
}

// truncateRunes caps s at maxRunes runes, not bytes: s[:maxRunes] on the raw
// string can slice a multi-byte character (an accented letter, an emoji) in
// half, leaving invalid UTF-8 that renders as garbled trailing characters
// once stored and redisplayed.
func truncateRunes(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes])
}

func correctWord(correct bool) string {
	if correct {
		return "right"
	}
	return "wrong"
}

// describeGuessPublic is how a judged guess reads in chat at reveal.
// g.JudgedByAI is the persisted guess.Verdict.ByAI, so an AI-judged guess gets the
// AI-Quizmaster phrasing here instead of a match percent.
func describeGuessPublic(g database.Guess) string {
	if !g.Judged {
		return "couldn't be judged"
	}
	titleWord := correctWord(g.TitleCorrect)
	artistWord := correctWord(g.ArtistCorrect)
	if g.JudgedByAI {
		return fmt.Sprintf("title %s, artist %s — judged by the AI Quizmaster", titleWord, artistWord)
	}
	return fmt.Sprintf("title %s (%d%% match), artist %s (%d%% match)",
		titleWord, g.TitleMatchPercent, artistWord, g.ArtistMatchPercent)
}

// announceGuesses puts every judged guess of the round in chat, in the order the
// songs were played. A guess about a song skipped earlier in the round says which
// song it was about, since several songs can share one round.
func announceGuesses(lobbyId uuid.UUID, guesses []database.Guess) {
	for _, g := range guesses {
		on := ""
		if !g.IsCurrentSong {
			on = fmt.Sprintf(" on the skipped “%s”", esc(g.SongTitle))
		}
		announce(lobbyId, fmt.Sprintf(
			"<blue>%s</> guessed “%s”%s — %s",
			esc(g.PlayerName), esc(g.GuessText), on, describeGuessPublic(g),
		))
	}
}

// SkipCard spends a token to abandon the song in play and draw a replacement.
// A card with a confirmed-broken video is already excluded from the draw pile
// entirely, so skip's remaining purpose is "I don't want to/can't judge this
// one for some other reason" — a token is the right disincentive for that.
// ReportDeadVideo is the runtime fallback for a song whose video will not
// play. The YouTube IFrame player reports 100 (gone), 101/150 (embedding
// disabled) and 2 (malformed id) definitively, so a client that hits one of
// those knows the video is unplayable even when the Data API check never ran
// — a missing API key, a rate limit, or a video that died since the last
// check. The player's own browser becomes the availability oracle.
//
// Free, and not counted as the turn player's skip: a dead link is the
// catalogue's fault, not a choice they made. The card is also recorded
// unavailable so every future draw pile excludes it, which is the same state
// a Data API check would have written.
func ReportDeadVideo(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if challengeInProgress(w, ctx) {
		return
	}

	if ctx.Game.GameStatus != database.StatusActive {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The game is not running."))
		return
	}
	if ctx.Game.RoundPhase != database.PhaseListening {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The song can only be replaced before a placement is locked in."))
		return
	}
	// Only the player on turn reports it, even though every client's player
	// hits the same error: otherwise a full lobby fires this at once and
	// races to skip several songs instead of one.
	if !ctx.Game.CurrentPlayerId.Valid || ctx.Game.CurrentPlayerId.UUID != ctx.Player.Id {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Only the player on turn reports a dead video."))
		return
	}

	card, err := database.GetCurrentCard(ctx.Game.Id)
	if err != nil || card.CardId == uuid.Nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("No song is in play."))
		return
	}

	// Duration 0: whatever length was on record is meaningless for a video
	// that no longer plays, and it is re-measured if it ever comes back.
	if err := database.SetVideoStatus(card.CardId, false, 0); err != nil {
		log.Println("failed to record dead video:", err)
	}
	if err := database.PruneUnavailableFromDrawPile(ctx.Game.Id); err != nil {
		log.Println("failed to prune dead video from draw pile:", err)
	}

	// A song that would not play was nobody's fault and never really played,
	// so guesses made on it are dropped rather than judged.
	if err := database.SkipCurrentCard(ctx.Game.Id, false); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to replace the song."))
		return
	}

	gsWebsocket.LobbyBroadcast(ctx.LobbyId, "songStop")
	apiRoom.MirrorBroadcast(ctx.LobbyId, "songStop")
	announce(ctx.LobbyId, "<red>That song's video would not play</> — swapped for another, no token spent")
	sendStatus(ctx.LobbyId, "That video is unavailable — a new song has been drawn.")
	refresh(ctx.LobbyId)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Replaced."))
}

func SkipCard(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if challengeInProgress(w, ctx) {
		return
	}

	if ctx.Game.GameStatus != database.StatusActive {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The game is not running."))
		return
	}
	if ctx.Game.RoundPhase != database.PhaseListening {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The song can only be skipped before a placement is locked in."))
		return
	}
	if !ctx.Game.CurrentPlayerId.Valid || ctx.Game.CurrentPlayerId.UUID != ctx.Player.Id {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Only the player on turn can skip."))
		return
	}

	// Room mode: locking a title/artist guess commits you to this song — skip
	// would otherwise let someone name it then fish for an easier card.
	if isRoom, roomErr := database.LobbyIsRoom(ctx.LobbyId); roomErr == nil && isRoom {
		if guessed, guessErr := database.HasGuessed(ctx.Game.Id, ctx.Player.Id); guessErr == nil && guessed {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("You locked in a guess — skip is disabled for this song."))
			return
		}
	}

	tokens, err := database.GetPlayerTokens(ctx.Game.Id, ctx.Player.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check your tokens."))
		return
	}
	if tokens < database.SkipCost {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("You need " + tokenCount(database.SkipCost) + " to skip."))
		return
	}
	if _, err := database.AddPlayerTokens(ctx.Game.Id, ctx.Player.Id, -database.SkipCost); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to spend your tokens."))
		return
	}

	// Read before the swap: once skipped, the song is out of play for good, so
	// telling the table what it was gives away nothing.
	skipped, skippedErr := database.GetCurrentCardAnswer(ctx.Game.Id)

	// The song was played, so what everyone guessed about it stays on file and
	// is judged, and paid, with the rest of the round.
	if err := database.SkipCurrentCard(ctx.Game.Id, true); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to skip the song."))
		return
	}

	gsWebsocket.LobbyBroadcast(ctx.LobbyId, "songStop")
	apiRoom.MirrorBroadcast(ctx.LobbyId, "songStop")
	skipLine := fmt.Sprintf("<blue>%s</> %s to skip a song", esc(ctx.Player.Name), tokensSpent(database.SkipCost))
	if skippedErr == nil && skipped.CardId != uuid.Nil {
		skipLine += fmt.Sprintf(": “%s” by %s (%d)", esc(skipped.Title), esc(skipped.Artist), skipped.ReleaseYear)
	}
	announce(ctx.LobbyId, skipLine)
	sendStatus(ctx.LobbyId, "Song skipped — a new one has been drawn.")
	refresh(ctx.LobbyId)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Skipped."))
}

// TimeoutPass is called by the browser whose turn timer reached zero, for the
// listening phase only — the steal_join/steal_turn phases have their own
// server-side scheduled timeouts (see steal.go) since those need to fire even
// if every client's tab is closed, not just the turn player's. The server
// re-checks whose turn it is, so a stale or malicious call cannot end
// somebody else's turn.
func TimeoutPass(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if challengeInProgress(w, ctx) {
		return
	}

	if ctx.Game.GameStatus != database.StatusActive {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Nothing to time out."))
		return
	}

	switch ctx.Game.RoundPhase {
	case database.PhaseListening:
		if !ctx.Game.CurrentPlayerId.Valid || ctx.Game.CurrentPlayerId.UUID != ctx.Player.Id {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Not your turn to time out."))
			return
		}
		// The player on turn never committed, so there is nothing to judge and
		// nobody can win the card. Discard it and move on.
		gsWebsocket.LobbyBroadcast(ctx.LobbyId, "songStop")
		apiRoom.MirrorBroadcast(ctx.LobbyId, "songStop")
		announce(ctx.LobbyId, fmt.Sprintf("<red>%s</> ran out of time", esc(ctx.Player.Name)))
		if card, err := database.GetCurrentCard(ctx.Game.Id); err == nil && card.CardId != uuid.Nil {
			if logErr := database.LogCardEvent(card.CardId, database.CardEventDiscarded); logErr != nil {
				log.Println(logErr)
			}
		}
		if err := database.ClearPlacements(ctx.Game.Id); err != nil {
			log.Println(err)
		}
		announceGuessesAtTimeout(ctx)
		if err := database.ClearGuesses(ctx.Game.Id); err != nil {
			log.Println(err)
		}
		if err := database.AdvanceToNextPlayer(ctx.Game.Id); err != nil {
			log.Println(err)
			announce(ctx.LobbyId, "<red>The draw pile is empty</> — no more songs to play")
			gsWebsocket.LobbyBroadcast(ctx.LobbyId, "reload")
			apiRoom.MirrorBroadcast(ctx.LobbyId, "reload")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Out of songs."))
			return
		}
		sendStatus(ctx.LobbyId, fmt.Sprintf("%s ran out of time. %s is up.", ctx.Player.Name, currentPlayerName(ctx.Game.Id)))
		refresh(ctx.LobbyId)
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Turn passed."))
}

// announceGuessesAtTimeout settles a round that ended with nobody placing the
// song. The guesses typed along the way are still the table's attempts, so they
// are judged, paid and put in chat like any round's, and the song they were about
// is named so the verdicts mean something.
func announceGuessesAtTimeout(ctx gameContext) {
	guesses, err := database.SettleGuesses(ctx.Game.Id)
	if err != nil {
		log.Println(err)
		return
	}
	if len(guesses) == 0 {
		return
	}
	if card, err := database.GetCurrentCardAnswer(ctx.Game.Id); err == nil && card.CardId != uuid.Nil {
		announce(ctx.LobbyId, fmt.Sprintf("The song was %s — “%s” (%d)", esc(card.Artist), esc(card.Title), card.ReleaseYear))
	}
	announceGuesses(ctx.LobbyId, guesses)
	for _, g := range guesses {
		if earned := database.GuessTokensEarned(g); g.Judged && earned > 0 {
			announce(ctx.LobbyId, fmt.Sprintf("<green>%s</> earned %s for their guess", esc(g.PlayerName), tokenCount(earned)))
		}
	}
}

// SetLobbyMessage updates the pinned lobby note.
func SetLobbyMessage(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	message := strings.TrimSpace(r.FormValue("message"))
	if err := gsDatabase.SetLobbyMessage(ctx.LobbyId, message); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to set the lobby message."))
		return
	}

	gsWebsocket.LobbyBroadcast(ctx.LobbyId, "lobbyMessage:"+message)
	apiRoom.MirrorBroadcast(ctx.LobbyId, "lobbyMessage:"+message)
	if message == "" {
		announce(ctx.LobbyId, fmt.Sprintf("<blue>%s</> cleared the lobby message", esc(ctx.Player.Name)))
	} else {
		announce(ctx.LobbyId, fmt.Sprintf("<blue>%s</> set the lobby message: %s", esc(ctx.Player.Name), esc(message)))
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Lobby message updated."))
}
