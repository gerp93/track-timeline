package apiTrackTimeline

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	gsDatabase "github.com/gerp93/gameshell-framework/database"
	gsWebsocket "github.com/gerp93/gameshell-framework/websocket"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/guess"
)

// PlaceCard records the placement of the player on turn and opens the challenge
// window. If nobody is in a position to challenge, the round resolves straight
// away rather than making everyone wait out a window nobody can act in.
func PlaceCard(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
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

	if err := database.CommitPlacement(ctx.Game.Id, ctx.Player.Id, position, false); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("You have already placed this song."))
		return
	}

	// The song stops the moment a placement is locked in: leaving it playing
	// through the challenge window would hand challengers more listening time
	// than the player on turn got.
	gsWebsocket.LobbyBroadcast(ctx.LobbyId, "songStop")

	outstanding, err := database.ChallengersOutstanding(ctx.Game.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check for challengers."))
		return
	}

	if outstanding == 0 {
		resolveAndAnnounce(ctx)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Placed."))
		return
	}

	if err := database.SetRoundPhase(ctx.Game.Id, database.PhaseChallenge); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to open the challenge window."))
		return
	}

	sendStatus(ctx.LobbyId, fmt.Sprintf("%s has placed the song. Spend a token to challenge?", ctx.Player.Name))
	announce(ctx.LobbyId, fmt.Sprintf("<blue>%s</> locked in a placement — challenge window open", esc(ctx.Player.Name)))
	refresh(ctx.LobbyId)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Placed."))
}

// Challenge spends a token to place the same song somewhere in the challenger's
// own timeline. The window closes once nobody else can act.
func Challenge(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	if ctx.Game.RoundPhase != database.PhaseChallenge {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("There is no challenge window open."))
		return
	}
	if ctx.Game.CurrentPlayerId.Valid && ctx.Game.CurrentPlayerId.UUID == ctx.Player.Id {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("You cannot challenge your own placement."))
		return
	}

	tokens, err := database.GetPlayerTokens(ctx.Game.Id, ctx.Player.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check your tokens."))
		return
	}
	if tokens < 1 {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("You have no tokens to spend."))
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

	// Record the placement before spending the token: the unique constraint on
	// the placement is what stops a double challenge, so charging first would
	// let a duplicate request cost a token for nothing.
	if err := database.CommitPlacement(ctx.Game.Id, ctx.Player.Id, position, true); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("You have already challenged this song."))
		return
	}
	if _, err := database.AddPlayerTokens(ctx.Game.Id, ctx.Player.Id, -1); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to spend your token."))
		return
	}

	announce(ctx.LobbyId, fmt.Sprintf("<red>%s</> spent a token to challenge", esc(ctx.Player.Name)))

	outstanding, err := database.ChallengersOutstanding(ctx.Game.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check for challengers."))
		return
	}

	if outstanding == 0 {
		resolveAndAnnounce(ctx)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Challenge placed."))
		return
	}

	sendStatus(ctx.LobbyId, fmt.Sprintf("%s challenged. Waiting on %d more.", ctx.Player.Name, outstanding))
	refresh(ctx.LobbyId)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Challenge placed."))
}

// CloseChallenge ends the window early. Anyone in the lobby may call it, since
// its only effect is to stop waiting on players who have decided not to act.
func CloseChallenge(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}

	if ctx.Game.RoundPhase != database.PhaseChallenge {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("There is no challenge window open."))
		return
	}

	resolveAndAnnounce(ctx)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Revealed."))
}

// resolveAndAnnounce reveals the answer, awards the card, checks for a winner
// and either ends the game or moves the turn on.
func resolveAndAnnounce(ctx gameContext) {
	outcome, err := database.ResolveRound(ctx.Game.Id)
	if err != nil {
		log.Println(err)
		sendStatus(ctx.LobbyId, "Something went wrong revealing the song.")
		return
	}

	payload := resultPayload{
		Title:          outcome.Title,
		Artist:         outcome.Artist,
		ReleaseYear:    outcome.ReleaseYear,
		WinnerName:     outcome.WinnerName,
		WonByChallenge: outcome.WonByChallenge,
	}

	songLine := fmt.Sprintf("%s — %s (%d)", esc(outcome.Artist), esc(outcome.Title), outcome.ReleaseYear)

	if outcome.WinnerPlayerId.Valid {
		payload.Type = "won"
		if outcome.WonByChallenge {
			payload.BottomMessage = fmt.Sprintf("%s stole it with a challenge! %s — %s (%d)",
				outcome.WinnerName, outcome.Artist, outcome.Title, outcome.ReleaseYear)
			announce(ctx.LobbyId, fmt.Sprintf("<green>%s</> stole the card with a challenge — %s", esc(outcome.WinnerName), songLine))
		} else {
			payload.BottomMessage = fmt.Sprintf("%s placed it correctly. %s — %s (%d)",
				outcome.WinnerName, outcome.Artist, outcome.Title, outcome.ReleaseYear)
			announce(ctx.LobbyId, fmt.Sprintf("<green>%s</> placed it correctly — %s", esc(outcome.WinnerName), songLine))
		}
	} else {
		payload.Type = "discarded"
		payload.BottomMessage = fmt.Sprintf("Nobody got it. %s — %s (%d)",
			outcome.Artist, outcome.Title, outcome.ReleaseYear)
		announce(ctx.LobbyId, fmt.Sprintf("<red>Nobody placed it correctly</> — %s", songLine))
	}

	winnerUserId, err := database.CheckWinner(ctx.Game.Id)
	if err != nil {
		log.Println(err)
	}

	if winnerUserId != uuid.Nil {
		payload.GameOver = true
		payload.UserId = winnerUserId.String()
		if user, err := gsDatabase.GetUser(winnerUserId); err == nil {
			payload.BottomMessage = fmt.Sprintf("%s wins! %s — %s (%d)",
				user.Name, outcome.Artist, outcome.Title, outcome.ReleaseYear)
			announce(ctx.LobbyId, fmt.Sprintf("<green>%s wins the game!</>", esc(user.Name)))
		}
		if celebration, err := gsDatabase.GetUserWinCelebration(winnerUserId); err == nil {
			if celebration.Message.Valid {
				payload.Celebration = celebration.Message.String
			}
			payload.HasGif = celebration.HasGif
		}
		sendResult(ctx.LobbyId, payload)
		gsWebsocket.LobbyBroadcast(ctx.LobbyId, "reload")
		return
	}

	if err := database.AdvanceToNextPlayer(ctx.Game.Id); err != nil {
		log.Println(err)
		// A dry draw pile is the expected way to get here.
		payload.BottomMessage += " No songs left — the game is over."
		sendResult(ctx.LobbyId, payload)
		announce(ctx.LobbyId, "<red>The draw pile is empty</> — no more songs to play")
		gsWebsocket.LobbyBroadcast(ctx.LobbyId, "reload")
		return
	}

	payload.NextPlayerName = currentPlayerName(ctx.Game.Id)
	sendResult(ctx.LobbyId, payload)
	refresh(ctx.LobbyId)
}

// SubmitGuess judges a free-form artist/title guess and awards tokens. Guesses
// are judged as they arrive rather than at the reveal, so a token earned on
// this song can still be spent challenging it — which is the tension the whole
// economy is built around.
func SubmitGuess(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
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
	if ctx.Game.RoundPhase == database.PhaseReveal {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The song has already been revealed."))
		return
	}

	guessText := strings.TrimSpace(r.FormValue("guess"))
	if guessText == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Type a guess first."))
		return
	}
	if len(guessText) > 500 {
		guessText = guessText[:500]
	}

	already, err := database.HasGuessed(ctx.Game.Id, ctx.Player.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check your guess."))
		return
	}
	if already {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("You have already guessed this song."))
		return
	}

	// The answer is read server-side and compared here; it never leaves this
	// handler.
	card, err := database.GetCurrentCardAnswer(ctx.Game.Id)
	if err != nil || card.CardId == uuid.Nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("No song is in play."))
		return
	}

	verdict := guess.Adjudicate(r.Context(), guess.Input{
		Guess:  guessText,
		Title:  card.Title,
		Artist: card.Artist,
	})

	tokensAwarded := 0
	if verdict.TitleCorrect {
		tokensAwarded++
	}
	if verdict.ArtistCorrect {
		tokensAwarded++
	}

	if err := database.RecordGuess(ctx.Game.Id, ctx.Player.Id, guessText, verdict.TitleCorrect, verdict.ArtistCorrect, tokensAwarded); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to record your guess."))
		return
	}
	if logErr := database.LogTitleGuess(ctx.UserId, card.CardId, guessText, verdict.TitleCorrect, verdict.ArtistCorrect); logErr != nil {
		log.Println(logErr)
	}

	if tokensAwarded > 0 {
		if _, err := database.AddPlayerTokens(ctx.Game.Id, ctx.Player.Id, tokensAwarded); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to award your token."))
			return
		}
	}

	// What was right goes only to the player who guessed — telling the lobby
	// "Ada got the artist" narrows it down for everyone still thinking.
	private := describeVerdict(verdict, tokensAwarded)
	if verdict.Explanation != "" {
		private += " " + verdict.Explanation
	}
	gsWebsocket.PlayerBroadcast(ctx.Player.Id, "alert:"+private)

	// The lobby hears only that a guess scored, never what it contained.
	if tokensAwarded > 0 {
		announce(ctx.LobbyId, fmt.Sprintf("<green>%s</> earned %s", esc(ctx.Player.Name), tokenWord(tokensAwarded)))
		refresh(ctx.LobbyId)
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(private))
}

func describeVerdict(verdict guess.Verdict, tokensAwarded int) string {
	switch {
	case verdict.TitleCorrect && verdict.ArtistCorrect:
		return "Title and artist both right — " + tokenWord(tokensAwarded) + "."
	case verdict.TitleCorrect:
		return "Title right, artist wrong — " + tokenWord(tokensAwarded) + "."
	case verdict.ArtistCorrect:
		return "Artist right, title wrong — " + tokenWord(tokensAwarded) + "."
	default:
		return "Not this time."
	}
}

func tokenWord(count int) string {
	if count == 1 {
		return "1 token"
	}
	return fmt.Sprintf("%d tokens", count)
}

// SkipCard abandons the song in play and draws a replacement. This exists
// because a dead or region-locked video would otherwise wedge the round with no
// way out: nobody can place a song they cannot hear.
func SkipCard(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
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

	if err := database.SkipCurrentCard(ctx.Game.Id); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to skip the song."))
		return
	}

	gsWebsocket.LobbyBroadcast(ctx.LobbyId, "songStop")
	announce(ctx.LobbyId, fmt.Sprintf("<blue>%s</> skipped a song that would not play", esc(ctx.Player.Name)))
	sendStatus(ctx.LobbyId, "Song skipped — a new one has been drawn.")
	refresh(ctx.LobbyId)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Skipped."))
}

// TimeoutPass is called by the browser whose turn timer reached zero. The
// server re-checks whose turn it is, so a stale or malicious call cannot end
// somebody else's turn.
func TimeoutPass(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}

	if ctx.Game.GameStatus != database.StatusActive {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Nothing to time out."))
		return
	}

	switch ctx.Game.RoundPhase {
	case database.PhaseChallenge:
		// Out of time to challenge: close the window and reveal.
		announce(ctx.LobbyId, "<red>Challenge window closed</> — time is up")
		resolveAndAnnounce(ctx)

	case database.PhaseListening:
		if !ctx.Game.CurrentPlayerId.Valid || ctx.Game.CurrentPlayerId.UUID != ctx.Player.Id {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Not your turn to time out."))
			return
		}
		// The player on turn never committed, so there is nothing to judge and
		// nobody can win the card. Discard it and move on.
		gsWebsocket.LobbyBroadcast(ctx.LobbyId, "songStop")
		announce(ctx.LobbyId, fmt.Sprintf("<red>%s</> ran out of time", esc(ctx.Player.Name)))
		if card, err := database.GetCurrentCard(ctx.Game.Id); err == nil && card.CardId != uuid.Nil {
			if logErr := database.LogCardEvent(card.CardId, database.CardEventDiscarded); logErr != nil {
				log.Println(logErr)
			}
		}
		if err := database.ClearPlacements(ctx.Game.Id); err != nil {
			log.Println(err)
		}
		if err := database.ClearGuesses(ctx.Game.Id); err != nil {
			log.Println(err)
		}
		if err := database.AdvanceToNextPlayer(ctx.Game.Id); err != nil {
			log.Println(err)
			announce(ctx.LobbyId, "<red>The draw pile is empty</> — no more songs to play")
			gsWebsocket.LobbyBroadcast(ctx.LobbyId, "reload")
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

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Lobby message updated."))
}
