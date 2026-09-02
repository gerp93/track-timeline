package apiCard

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	gsApi "github.com/gerp93/gameshell-framework/api"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
)

// maxImportUploadBytes bounds how much request body an import may send,
// independent of how many cards that JSON decodes to (database.MaxImportCards
// caps that separately). This stops a client making the server buffer and parse
// an arbitrarily large body at all.
const maxImportUploadBytes = 2 << 20 // 2 MiB

// hasDeckAccess writes its own response and returns false when the caller may
// not edit this deck, so handlers can guard with a single if.
func hasDeckAccess(w http.ResponseWriter, r *http.Request, deckId uuid.UUID) bool {
	userId := gsApi.GetUserId(r)
	if userId == uuid.Nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to get user id."))
		return false
	}
	ok, err := gsDatabase.UserHasDeckAccess(userId, deckId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check deck access."))
		return false
	}
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("User does not have access."))
		return false
	}
	return true
}

// parseReleaseYear turns the posted year into a nullable column value. Blank is
// allowed and means authored-but-incomplete: the card is kept but excluded from
// every draw pile until someone fills the year in.
func parseReleaseYear(value string) (sql.NullInt64, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return sql.NullInt64{}, ""
	}
	year, err := strconv.Atoi(value)
	if err != nil {
		return sql.NullInt64{}, "Release year must be a whole number."
	}
	if year < 1000 || year > 2999 {
		return sql.NullInt64{}, "Release year must be a four-digit year."
	}
	return sql.NullInt64{Int64: int64(year), Valid: true}, ""
}

// parseStartOffset reads the optional playback start offset in seconds.
func parseStartOffset(value string) (int, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, ""
	}
	seconds, err := strconv.Atoi(value)
	if err != nil {
		return 0, "Start offset must be a whole number of seconds."
	}
	if seconds < 0 {
		return 0, "Start offset cannot be negative."
	}
	return seconds, ""
}

// parseCategoryId parses the required categoryId form value and confirms it
// exists. CARD.CATEGORY_ID is a soft reference, so this check is the integrity
// constraint.
func parseCategoryId(value string) (uuid.NullUUID, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return uuid.NullUUID{}, "A genre is required."
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.NullUUID{}, "Invalid genre."
	}
	exists, err := database.CategoryExists(id)
	if err != nil {
		return uuid.NullUUID{}, "Failed to check genre."
	}
	if !exists {
		return uuid.NullUUID{}, "Selected genre does not exist."
	}
	return uuid.NullUUID{UUID: id, Valid: true}, ""
}

// cardFormValues is everything the create and update forms share.
type cardFormValues struct {
	videoId      string
	startSeconds int
	title        string
	artist       string
	releaseYear  sql.NullInt64
	categoryId   uuid.NullUUID
}

// readCardForm validates the posted card fields, writing its own response and
// returning false on the first problem.
func readCardForm(w http.ResponseWriter, r *http.Request) (cardFormValues, bool) {
	var values cardFormValues

	values.title = strings.TrimSpace(r.FormValue("title"))
	if values.title == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("A title is required."))
		return values, false
	}

	values.artist = strings.TrimSpace(r.FormValue("artist"))
	if values.artist == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("An artist is required."))
		return values, false
	}

	videoId, err := database.ParseYouTubeVideoId(r.FormValue("videoId"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Paste a YouTube link or video ID."))
		return values, false
	}
	values.videoId = videoId

	startSeconds, message := parseStartOffset(r.FormValue("startSeconds"))
	if message != "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(message))
		return values, false
	}
	values.startSeconds = startSeconds

	releaseYear, message := parseReleaseYear(r.FormValue("year"))
	if message != "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(message))
		return values, false
	}
	values.releaseYear = releaseYear

	categoryId, message := parseCategoryId(r.FormValue("categoryId"))
	if message != "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(message))
		return values, false
	}
	values.categoryId = categoryId

	return values, true
}

func Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	deckId, err := uuid.Parse(r.FormValue("deckId"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid deck."))
		return
	}
	if !hasDeckAccess(w, r, deckId) {
		return
	}

	values, ok := readCardForm(w, r)
	if !ok {
		return
	}

	existingId, err := database.GetCardIdByVideo(deckId, values.videoId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check for an existing card."))
		return
	}
	if existingId != uuid.Nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("That song is already in this deck."))
		return
	}

	if _, err := database.CreateCard(
		deckId,
		values.videoId,
		values.startSeconds,
		values.title,
		values.artist,
		values.releaseYear,
		values.categoryId,
	); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to create card."))
		return
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Card created."))
}

func Update(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
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
	if !hasDeckAccess(w, r, card.DeckId) {
		return
	}

	values, ok := readCardForm(w, r)
	if !ok {
		return
	}

	// Only a collision with a *different* card is a duplicate; keeping the
	// card's own video id while editing the title is normal.
	existingId, err := database.GetCardIdByVideo(card.DeckId, values.videoId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check for an existing card."))
		return
	}
	if existingId != uuid.Nil && existingId != card.Id {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Another card in this deck already uses that video."))
		return
	}

	if err := database.UpdateCard(
		cardId,
		values.videoId,
		values.startSeconds,
		values.title,
		values.artist,
		values.releaseYear,
		values.categoryId,
	); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to update card."))
		return
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Card updated."))
}

func Delete(w http.ResponseWriter, r *http.Request) {
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
	if !hasDeckAccess(w, r, card.DeckId) {
		return
	}

	if err := database.DeleteCard(cardId); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to delete card."))
		return
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Card deleted."))
}

// GetCardExport writes the deck's cards as CSV. Headerless, matching the deck
// export in this repo's siblings, and column-for-column what the import format
// expects so an export can be edited and fed back in.
func GetCardExport(w http.ResponseWriter, r *http.Request) {
	deckId, err := uuid.Parse(r.PathValue("deckId"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid deck."))
		return
	}
	if !hasDeckAccess(w, r, deckId) {
		return
	}

	cards, err := database.GetCardsInDeckExport(deckId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get cards."))
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	writer := csv.NewWriter(w)
	defer writer.Flush()

	for _, card := range cards {
		year := ""
		if card.ReleaseYear.Valid {
			year = strconv.FormatInt(card.ReleaseYear.Int64, 10)
		}
		category := ""
		if card.CategoryName.Valid {
			category = card.CategoryName.String
		}
		record := []string{
			card.Title,
			card.Artist,
			year,
			card.YouTubeVideoId,
			strconv.Itoa(card.StartOffsetSeconds),
			category,
		}
		if err := writer.Write(record); err != nil {
			return
		}
	}
}

// ImportJSON accepts an uploaded .json array of cards and adds the ones not
// already in the deck.
func ImportJSON(w http.ResponseWriter, r *http.Request) {
	deckId, err := uuid.Parse(r.PathValue("deckId"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid deck."))
		return
	}
	if !hasDeckAccess(w, r, deckId) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxImportUploadBytes)
	if err := r.ParseMultipartForm(maxImportUploadBytes); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The upload was too large or could not be read."))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("No file was uploaded."))
		return
	}
	defer file.Close()

	if !strings.HasSuffix(strings.ToLower(header.Filename), ".json") {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The file must be a .json file."))
		return
	}

	data, err := io.ReadAll(file)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to read the uploaded file."))
		return
	}

	entries, err := database.ParseCardImportJSON(data)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(err.Error()))
		return
	}

	result, err := database.ImportCardsIntoDeck(deckId, entries)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to import cards."))
		return
	}

	message := fmt.Sprintf("Imported %d card(s); skipped %d already in this deck.", result.Imported, result.Skipped)
	if result.Uncategorized > 0 {
		message += fmt.Sprintf(" %d had no matching genre and were left ungenred.", result.Uncategorized)
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(message))
}
