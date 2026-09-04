package apiCard

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	gsApi "github.com/gerp93/gameshell-framework/api"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/guess"
)

// TestGuess runs the local matcher and Claude against one card, side by side,
// without touching a game. Admin-only.
func TestGuess(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can test the match engine."))
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	cardId, err := uuid.Parse(strings.TrimSpace(r.FormValue("cardId")))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Pick a song first."))
		return
	}
	card, err := database.GetCard(cardId)
	if err != nil || card.Id == uuid.Nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("That song was not found."))
		return
	}

	guessMode := strings.TrimSpace(r.FormValue("guessMode"))
	if guessMode == "" {
		guessMode = database.GuessModeBoth
	}
	if err := database.ValidateGuessMode(guessMode); err != nil || guessMode == database.GuessModeOff {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Guess mode must be both, title, or either."))
		return
	}

	matchPercent := database.DefaultGuessMatchPercent
	if raw := strings.TrimSpace(r.FormValue("guessMatchPercent")); raw != "" {
		matchPercent, err = strconv.Atoi(raw)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Match required must be 60, 70, 80, or 90."))
			return
		}
	}
	if err := database.ValidateGuessMatchPercent(matchPercent); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Match required must be 60, 70, 80, or 90."))
		return
	}

	titleGuess := strings.TrimSpace(r.FormValue("guessTitle"))
	artistGuess := strings.TrimSpace(r.FormValue("guessArtist"))
	if guessMode == database.GuessModeTitle {
		artistGuess = ""
	}
	if titleGuess == "" && artistGuess == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Type a guess first."))
		return
	}

	combined := titleGuess
	if titleGuess != "" && artistGuess != "" {
		combined = titleGuess + " by " + artistGuess
	} else if artistGuess != "" {
		combined = artistGuess
	}

	in := guess.Input{
		Guess:           combined,
		TitleGuess:      titleGuess,
		ArtistGuess:     artistGuess,
		Title:           card.Title,
		Artist:          card.Artist,
		MinMatchPercent: matchPercent,
		TitleOnly:       guessMode == database.GuessModeTitle,
	}

	local, localErr := guess.Normalized{}.Judge(r.Context(), in)

	claudeCtx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	claude, claudeErr := guess.AdjudicateClaude(claudeCtx, in)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<p>Heuristic bar %d%%. Naming mode: %s.</p>\n", matchPercent, html.EscapeString(guessMode))
	_, _ = w.Write([]byte("<div class=\"guess-compare\">\n"))
	writeJudgeColumn(w, "Heuristic", local, localErr, guessMode)
	writeJudgeColumn(w, "Claude", claude, claudeErr, guessMode)
	_, _ = w.Write([]byte("</div>\n"))

	if localErr == nil && claudeErr == nil {
		sameTitle := local.TitleCorrect == claude.TitleCorrect
		sameArtist := guessMode == database.GuessModeTitle || local.ArtistCorrect == claude.ArtistCorrect
		if sameTitle && sameArtist {
			_, _ = w.Write([]byte("<p class=\"guess-verdict-ok\">They agree on the token call.</p>\n"))
		} else {
			_, _ = w.Write([]byte("<p class=\"guess-verdict-no\"><strong>They disagree.</strong></p>\n"))
		}
	}
}

func writeJudgeColumn(w http.ResponseWriter, heading string, verdict guess.Verdict, err error, guessMode string) {
	fmt.Fprintf(w, "<div><h3>%s</h3>\n", html.EscapeString(heading))
	if err != nil {
		fmt.Fprintf(w, "<p class=\"guess-verdict-no\">%s</p></div>\n", html.EscapeString(err.Error()))
		return
	}
	fmt.Fprintf(w, "<p>Title: %s (%.0f%%)</p>\n", correctWord(verdict.TitleCorrect), verdict.TitleMatchPercent)
	if guessMode != database.GuessModeTitle {
		fmt.Fprintf(w, "<p>Artist: %s (%.0f%%)</p>\n", correctWord(verdict.ArtistCorrect), verdict.ArtistMatchPercent)
	}
	if database.GuessQualifies(database.Guess{
		TitleCorrect:  verdict.TitleCorrect,
		ArtistCorrect: verdict.ArtistCorrect,
	}, guessMode) {
		_, _ = w.Write([]byte("<p class=\"guess-verdict-ok\"><strong>Would win the token.</strong></p>\n"))
	} else {
		_, _ = w.Write([]byte("<p class=\"guess-verdict-no\"><strong>Would not win the token.</strong></p>\n"))
	}
	_, _ = w.Write([]byte("</div>\n"))
}

func correctWord(ok bool) string {
	if ok {
		return "right"
	}
	return "wrong"
}
