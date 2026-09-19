package apiCard

import (
	"context"
	"fmt"
	"html"
	"net/http"
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

	titleGuess := strings.TrimSpace(r.FormValue("guessTitle"))
	artistGuess := strings.TrimSpace(r.FormValue("guessArtist"))
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
		Guess:       combined,
		TitleGuess:  titleGuess,
		ArtistGuess: artistGuess,
		Title:       card.Title,
		Artist:      card.Artist,
	}

	local, localErr := guess.Normalized{}.Judge(r.Context(), in)

	claudeCtx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	claude, claudeErr := guess.AdjudicateClaude(claudeCtx, in)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<div class=\"guess-compare\">\n"))
	writeJudgeColumn(w, "Heuristic", local, localErr)
	writeJudgeColumn(w, "Claude", claude, claudeErr)
	_, _ = w.Write([]byte("</div>\n"))

	if localErr == nil && claudeErr == nil {
		sameTitle := local.TitleCorrect == claude.TitleCorrect
		sameArtist := local.ArtistCorrect == claude.ArtistCorrect
		if sameTitle && sameArtist {
			_, _ = w.Write([]byte("<p class=\"guess-verdict-ok\">They agree on the token call.</p>\n"))
		} else {
			_, _ = w.Write([]byte("<p class=\"guess-verdict-no\"><strong>They disagree.</strong></p>\n"))
		}
	}
}

func writeJudgeColumn(w http.ResponseWriter, heading string, verdict guess.Verdict, err error) {
	fmt.Fprintf(w, "<div><h3>%s</h3>\n", html.EscapeString(heading))
	if err != nil {
		fmt.Fprintf(w, "<p class=\"guess-verdict-no\">%s</p></div>\n", html.EscapeString(err.Error()))
		return
	}
	fmt.Fprintf(w, "<p>Title: %s (%.0f%%)</p>\n", correctWord(verdict.TitleCorrect), verdict.TitleMatchPercent)
	fmt.Fprintf(w, "<p>Artist: %s (%.0f%%)</p>\n", correctWord(verdict.ArtistCorrect), verdict.ArtistMatchPercent)
	if verdict.Raw != "" {
		fmt.Fprintf(w, "<p>Raw reply: <code>%s</code></p>\n", html.EscapeString(verdict.Raw))
	}
	earned := database.GuessTokensEarned(database.Guess{
		TitleCorrect:  verdict.TitleCorrect,
		ArtistCorrect: verdict.ArtistCorrect,
	})
	if earned > 0 {
		fmt.Fprintf(w, "<p class=\"guess-verdict-ok\"><strong>Would earn %s.</strong></p>\n", database.CurrentEconomy().Tokens(earned))
	} else {
		_, _ = w.Write([]byte("<p class=\"guess-verdict-no\"><strong>Would earn no tokens.</strong></p>\n"))
	}
	_, _ = w.Write([]byte("</div>\n"))
}

func correctWord(ok bool) string {
	if ok {
		return "right"
	}
	return "wrong"
}
