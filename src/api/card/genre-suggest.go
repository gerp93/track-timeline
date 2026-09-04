package apiCard

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	gsApi "github.com/gerp93/gameshell-framework/api"
	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/guess"
	"github.com/google/uuid"
)

func genreNames(categories []database.Category) []string {
	names := make([]string, 0, len(categories))
	for _, c := range categories {
		names = append(names, c.Name)
	}
	return names
}

func categoryIDForName(categories []database.Category, name string) (uuid.UUID, bool) {
	for _, c := range categories {
		if c.Name == name {
			return c.Id, true
		}
	}
	return uuid.Nil, false
}

// assignGenreViaClaude picks a genre, saves it, and always appends a log row.
func assignGenreViaClaude(ctx context.Context, card database.UngenredCard, categories []database.Category) (string, error) {
	name, err := guess.SuggestGenre(ctx, card.Title, card.Artist, genreNames(categories))
	if err != nil {
		if logErr := database.LogGenreAssign(card, "", false, err.Error()); logErr != nil {
			log.Println(logErr)
		}
		return "", err
	}
	id, ok := categoryIDForName(categories, name)
	if !ok {
		err := fmt.Errorf("genre %q not found", name)
		if logErr := database.LogGenreAssign(card, name, false, err.Error()); logErr != nil {
			log.Println(logErr)
		}
		return "", err
	}
	if err := database.UpdateCardCategory(card.CardId, id); err != nil {
		if logErr := database.LogGenreAssign(card, name, false, err.Error()); logErr != nil {
			log.Println(logErr)
		}
		return "", err
	}
	if logErr := database.LogGenreAssign(card, name, true, ""); logErr != nil {
		log.Println(logErr)
	}
	return name, nil
}

func redirectToGenreLog(w http.ResponseWriter) {
	w.Header().Set("HX-Redirect", "/videos?tab=genres&genre=log")
}

// SuggestGenre asks Claude for a genre and saves it on the card.
func SuggestGenre(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can assign genres via the Claude API."))
		return
	}
	if !guess.ClaudeConfigured() {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Claude API key is not configured."))
		return
	}

	cardId, err := uuid.Parse(r.PathValue("cardId"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid card."))
		return
	}

	card, err := database.GetCard(cardId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get card."))
		return
	}
	if card.Id == uuid.Nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("No card found."))
		return
	}

	deckName, err := database.DeckName(card.DeckId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get deck."))
		return
	}

	categories, err := database.GetCategories()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get genres."))
		return
	}
	if len(categories) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Create at least one genre before calling the Claude API."))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	ungenred := database.UngenredCard{
		CardId:   card.Id,
		Title:    card.Title,
		Artist:   card.Artist,
		DeckId:   card.DeckId,
		DeckName: deckName,
	}
	name, err := assignGenreViaClaude(ctx, ungenred, categories)
	if err != nil {
		log.Println(err)
		redirectToGenreLog(w)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("Claude API could not assign a genre for this song. See the Claude API log."))
		return
	}

	redirectToGenreLog(w)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf("Claude API assigned %q.", name)))
}

// SuggestGenres asks Claude for a genre on every ungenred song matching search
// and saves each pick. Uses the Claude API once per song.
func SuggestGenres(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can assign genres via the Claude API."))
		return
	}
	if !guess.ClaudeConfigured() {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Claude API key is not configured."))
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}
	search := r.FormValue("search")

	categories, err := database.GetCategories()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get genres."))
		return
	}
	if len(categories) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Create at least one genre before calling the Claude API."))
		return
	}

	cards, err := database.ListUngenredCardsMatching(search)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to list ungenred songs."))
		return
	}
	if len(cards) == 0 {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("No ungenred songs to assign."))
		return
	}

	assigned := 0
	failed := 0
	for _, card := range cards {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		_, err := assignGenreViaClaude(ctx, card, categories)
		cancel()
		if err != nil {
			log.Println(err)
			failed++
			continue
		}
		assigned++
	}

	redirectToGenreLog(w)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf(
		"Claude API assigned %d genre(s). Failed: %d.",
		assigned, failed,
	)))
}
