package apiCard

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	gsApi "github.com/gerp93/gameshell-framework/api"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/videocheck"
)

// deadVideoExportRow is the JSON shape for repairing dead links offline.
// youtubeVideoId is intentionally blank on export so the repairer fills it in.
type deadVideoExportRow struct {
	Id             string `json:"id"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	Deck           string `json:"deck"`
	YouTubeVideoId string `json:"youtubeVideoId"`
}

type deadVideoImportRow struct {
	Id             string `json:"id"`
	YouTubeVideoId string `json:"youtubeVideoId"`
}

func parseBulkCardIds(w http.ResponseWriter, r *http.Request) ([]uuid.UUID, bool) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return nil, false
	}
	if len(r.Form["cardId"]) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Select at least one song."))
		return nil, false
	}

	cardIds := make([]uuid.UUID, 0, len(r.Form["cardId"]))
	for _, raw := range r.Form["cardId"] {
		cardId, err := uuid.Parse(raw)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Invalid card id."))
			return nil, false
		}
		cardIds = append(cardIds, cardId)
	}
	return cardIds, true
}

func updateVideoStatus(
	w http.ResponseWriter,
	r *http.Request,
	update func(uuid.UUID) error,
) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can update video status."))
		return
	}

	cardId, err := uuid.Parse(r.PathValue("cardId"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid card."))
		return
	}
	if err := update(cardId); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to update song."))
		return
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Updated song."))
}

func updateVideoStatuses(
	w http.ResponseWriter,
	r *http.Request,
	update func(uuid.UUID) error,
) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can update video status."))
		return
	}

	cardIds, ok := parseBulkCardIds(w, r)
	if !ok {
		return
	}
	for _, cardId := range cardIds {
		if err := update(cardId); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to update songs."))
			return
		}
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf("Updated %d song(s).", len(cardIds))))
}

func MarkVideoOk(w http.ResponseWriter, r *http.Request) {
	updateVideoStatus(w, r, database.MarkVideoOk)
}

func MarkVideosOk(w http.ResponseWriter, r *http.Request) {
	updateVideoStatuses(w, r, database.MarkVideoOk)
}

func MarkVideoUnavailable(w http.ResponseWriter, r *http.Request) {
	updateVideoStatus(w, r, database.MarkVideoUnavailable)
}

func MarkVideosUnavailable(w http.ResponseWriter, r *http.Request) {
	updateVideoStatuses(w, r, database.MarkVideoUnavailable)
}

func MarkVideoIncorrect(w http.ResponseWriter, r *http.Request) {
	updateVideoStatus(w, r, database.MarkVideoIncorrect)
}

func MarkVideosIncorrect(w http.ResponseWriter, r *http.Request) {
	updateVideoStatuses(w, r, database.MarkVideoIncorrect)
}

func DeleteCards(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can delete songs."))
		return
	}

	cardIds, ok := parseBulkCardIds(w, r)
	if !ok {
		return
	}
	for _, cardId := range cardIds {
		if err := database.DeleteCard(cardId); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to delete songs."))
			return
		}
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf("Deleted %d song(s).", len(cardIds))))
}

// UpdateVideo changes only a card's YouTube link and marks it awaiting
// validation. Admin dead-video page and (via deck access) anyone who can edit
// the deck may call it.
func UpdateVideo(w http.ResponseWriter, r *http.Request) {
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
	if !gsApi.UserIsAdmin(r) && !hasDeckAccess(w, r, card.DeckId) {
		return
	}

	videoId, err := database.ParseYouTubeVideoId(r.FormValue("videoId"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("A valid YouTube video id or URL is required."))
		return
	}

	existingId, err := database.GetCardIdByVideo(card.DeckId, videoId)
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

	if err := database.UpdateCardYouTubeVideoId(cardId, videoId); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to update the video link."))
		return
	}
	if err := database.MarkVideoAwaitingValidation(cardId); err != nil {
		log.Println(err)
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Link saved — awaiting validation."))
}

// FindVideo searches YouTube for one card and saves the preferred result.
func FindVideo(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can find videos."))
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
	if card.VideoAwaiting {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Find via API is only for unavailable or incorrect videos."))
		return
	}

	hit, err := videocheck.SearchMusicVideo(r.Context(), card.Artist, card.Title)
	if err != nil {
		log.Println(err)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(videocheck.UserMessage(err)))
		return
	}
	if err := applyFoundVideo(card, hit); err != nil {
		log.Println(err)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to apply the found video: " + err.Error() + "."))
		return
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Found and saved a video. Run Resolve to validate it."))
}

// FindVideos searches YouTube for each selected card and saves each preferred
// result. Search calls are intentionally sequential so a large selection does
// not burst the YouTube API.
func FindVideos(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can find videos."))
		return
	}
	cardIds, ok := parseBulkCardIds(w, r)
	if !ok {
		return
	}

	found := 0
	skipped := 0
	for _, cardId := range cardIds {
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
		if card.VideoAwaiting {
			skipped++
			continue
		}

		hit, err := videocheck.SearchMusicVideo(r.Context(), card.Artist, card.Title)
		if err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(fmt.Sprintf(
				"Found %d video(s), then stopped: %s",
				found, videocheck.UserMessage(err),
			)))
			return
		}
		if err := applyFoundVideo(card, hit); err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(fmt.Sprintf(
				"Found %d video(s), then failed to apply the result for %s by %s: %s.",
				found, card.Title, card.Artist, err.Error(),
			)))
			return
		}
		found++
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf(
		"Found %d; skipped %d awaiting recheck.",
		found, skipped,
	)))
}

// ExportDeadVideos returns JSON for selected (or all) dead/awaiting cards.
// The youtubeVideoId field is blank so a repairer fills it in without
// accidentally reusing the dead id.
func ExportDeadVideos(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can export dead videos."))
		return
	}

	var cardIds []uuid.UUID
	if r.URL.Query().Get("all") == "1" {
		all, err := database.GetDeadOrAwaitingVideoIds(nil)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to list songs."))
			return
		}
		for _, cv := range all {
			cardIds = append(cardIds, cv.CardId)
		}
	} else {
		for _, raw := range r.URL.Query()["cardId"] {
			id, err := uuid.Parse(raw)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("Invalid card id."))
				return
			}
			cardIds = append(cardIds, id)
		}
	}
	if len(cardIds) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Select at least one song to export."))
		return
	}

	cards, err := database.GetCardsByIdsForExport(cardIds)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to load songs."))
		return
	}

	rows := make([]deadVideoExportRow, 0, len(cards))
	for _, c := range cards {
		rows = append(rows, deadVideoExportRow{
			Id:             c.CardId.String(),
			Title:          c.Title,
			Artist:         c.Artist,
			Deck:           c.DeckName,
			YouTubeVideoId: "",
		})
	}

	encoded, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to encode export."))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="dead-videos.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

// ImportVideoLinks updates ONLY the YouTube video id for each matching card
// id. Title, artist, year, genre, and deck are ignored even if present.
func ImportVideoLinks(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can import video links."))
		return
	}

	if err := r.ParseMultipartForm(maxImportUploadBytes); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to read the upload."))
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Choose a JSON file to import."))
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxImportUploadBytes))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to read the upload."))
		return
	}

	var rows []deadVideoImportRow
	if err := json.Unmarshal(data, &rows); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid JSON. Expected an array of {id, youtubeVideoId}."))
		return
	}
	if len(rows) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The import file is empty."))
		return
	}

	updated := 0
	for _, row := range rows {
		cardId, err := uuid.Parse(row.Id)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Invalid card id in import: " + row.Id + "."))
			return
		}
		videoId, err := database.ParseYouTubeVideoId(row.YouTubeVideoId)
		if err != nil || videoId == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Missing or invalid youtubeVideoId for " + row.Id + "."))
			return
		}

		card, err := database.GetCard(cardId)
		if err != nil || card.Id == uuid.Nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("No card found for id " + row.Id + "."))
			return
		}

		if err := database.UpdateCardYouTubeVideoId(cardId, videoId); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to update " + row.Id + "."))
			return
		}
		if err := database.MarkVideoAwaitingValidation(cardId); err != nil {
			log.Println(err)
		}
		updated++
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf("Updated %d video link(s). Run Resolve to validate them.", updated)))
}
