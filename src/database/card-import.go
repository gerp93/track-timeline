package database

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// MaxImportCards caps one upload. Large enough for a real deck, small enough
// that a malformed file cannot spend minutes inserting rows.
const MaxImportCards = 1000

// CardImportEntry is one record in an import file. The schema is enforced
// strictly (unknown fields are rejected) so a typo'd key fails loudly at import
// time rather than silently importing a card with a missing field.
type CardImportEntry struct {
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Year     *int   `json:"year"`
	VideoId  string `json:"videoId"`
	Category string `json:"category"`
	// MissingVideo is set during parse when videoId was empty or malformed.
	// The bad string is never kept — VideoId is cleared and the card is
	// imported with a NULL link, then marked unavailable for Dead Videos.
	MissingVideo bool `json:"-"`
}

// CardImportResult reports what an import actually did.
type CardImportResult struct {
	Imported                 int
	SkippedDuplicateVideo    int
	SkippedDuplicateMetadata int // same title + artist + year in this deck
	Failed                   int
	Uncategorized            int
	MissingVideo             int // imported with no usable YouTube link
}

// FormatImportReport is the human-readable summary shown after an import.
func (r CardImportResult) FormatImportReport() string {
	parts := []string{
		fmt.Sprintf("Imported: %d", r.Imported),
		fmt.Sprintf("Failed: %d", r.Failed),
		fmt.Sprintf("Skipped (duplicate video ID): %d", r.SkippedDuplicateVideo),
		fmt.Sprintf("Skipped (duplicate title/artist/year): %d", r.SkippedDuplicateMetadata),
	}
	if r.MissingVideo > 0 {
		parts = append(parts, fmt.Sprintf("Imported with no video link (Dead Videos): %d", r.MissingVideo))
	}
	if r.Uncategorized > 0 {
		parts = append(parts, fmt.Sprintf("Imported without a matching genre: %d", r.Uncategorized))
	}
	return strings.Join(parts, "\n")
}

// ParseCardImportJSON validates an uploaded file into entries. Every problem is
// reported against the entry's index so an author can find it in the file.
// Malformed or blank videoIds do not fail the file — those entries are marked
// MissingVideo with VideoId cleared.
func ParseCardImportJSON(data []byte) ([]CardImportEntry, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var entries []CardImportEntry
	if err := decoder.Decode(&entries); err != nil {
		return nil, fmt.Errorf("could not read the file as an array of cards: %w", err)
	}
	if len(entries) == 0 {
		return nil, errors.New("the file contained no cards")
	}
	if len(entries) > MaxImportCards {
		return nil, fmt.Errorf("the file contained %d cards; the maximum is %d", len(entries), MaxImportCards)
	}

	for i := range entries {
		entries[i].Title = strings.TrimSpace(entries[i].Title)
		entries[i].Artist = strings.TrimSpace(entries[i].Artist)
		entries[i].Category = strings.TrimSpace(entries[i].Category)
		entries[i].VideoId = strings.TrimSpace(entries[i].VideoId)

		if entries[i].Title == "" {
			return nil, fmt.Errorf("card %d has no title", i+1)
		}
		if entries[i].Artist == "" {
			return nil, fmt.Errorf("card %d has no artist", i+1)
		}

		if entries[i].VideoId == "" {
			entries[i].MissingVideo = true
			continue
		}
		videoId, err := ParseYouTubeVideoId(entries[i].VideoId)
		if err != nil {
			entries[i].VideoId = ""
			entries[i].MissingVideo = true
			continue
		}
		entries[i].VideoId = videoId
	}

	return entries, nil
}

func importMetadataKey(title, artist string, year sql.NullInt64) string {
	yearPart := "null"
	if year.Valid {
		yearPart = strconv.FormatInt(year.Int64, 10)
	}
	return title + "\x00" + artist + "\x00" + yearPart
}

func entryReleaseYear(entry CardImportEntry) sql.NullInt64 {
	if entry.Year == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*entry.Year), Valid: true}
}

// loadDeckImportKeys returns existing video ids and title/artist/year keys in
// a deck so import can skip duplicates without a query per row.
func loadDeckImportKeys(deckId uuid.UUID) (videos map[string]bool, metadata map[string]bool, err error) {
	rows, err := query(`
		SELECT COALESCE(YOUTUBE_VIDEO_ID, ''), TITLE, ARTIST, RELEASE_YEAR
		FROM CARD
		WHERE DECK_ID = ?
	`, deckId)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	videos = make(map[string]bool)
	metadata = make(map[string]bool)
	for rows.Next() {
		var videoId, title, artist string
		var year sql.NullInt64
		if err := rows.Scan(&videoId, &title, &artist, &year); err != nil {
			log.Println(err)
			return nil, nil, errors.New("failed to scan row in query results")
		}
		if videoId != "" {
			videos[videoId] = true
		}
		metadata[importMetadataKey(title, artist, year)] = true
	}
	return videos, metadata, nil
}

// ImportCardsIntoDeck inserts entries that are not already in the deck, matching
// categories by name. A card whose category name is unknown is imported without
// one rather than rejected: the alternative is failing an otherwise good file
// over a genre label. Cards with MissingVideo get a NULL YouTube id and are
// marked unavailable so they appear on Dead Videos for repair.
//
// Skips when the deck (or an earlier row in this file) already has the same
// video id, or the same title + artist + year.
func ImportCardsIntoDeck(deckId uuid.UUID, entries []CardImportEntry) (CardImportResult, error) {
	var result CardImportResult

	categories, err := GetCategories()
	if err != nil {
		return result, err
	}
	categoryByName := make(map[string]uuid.UUID, len(categories))
	for _, category := range categories {
		categoryByName[strings.ToLower(category.Name)] = category.Id
	}

	videos, metadata, err := loadDeckImportKeys(deckId)
	if err != nil {
		return result, err
	}

	for _, entry := range entries {
		releaseYear := entryReleaseYear(entry)
		metaKey := importMetadataKey(entry.Title, entry.Artist, releaseYear)

		if entry.VideoId != "" && videos[entry.VideoId] {
			result.SkippedDuplicateVideo++
			continue
		}
		if metadata[metaKey] {
			result.SkippedDuplicateMetadata++
			continue
		}

		var categoryId uuid.NullUUID
		if entry.Category != "" {
			if id, ok := categoryByName[strings.ToLower(entry.Category)]; ok {
				categoryId = uuid.NullUUID{UUID: id, Valid: true}
			} else {
				result.Uncategorized++
			}
		} else {
			result.Uncategorized++
		}

		cardId, err := CreateCard(deckId, entry.VideoId, entry.Title, entry.Artist, releaseYear, categoryId)
		if err != nil {
			log.Println(err)
			result.Failed++
			continue
		}
		if entry.MissingVideo {
			if err := MarkVideoUnavailable(cardId); err != nil {
				log.Println(err)
				result.Failed++
				_ = DeleteCard(cardId)
				continue
			}
			result.MissingVideo++
		}

		if entry.VideoId != "" {
			videos[entry.VideoId] = true
		}
		metadata[metaKey] = true
		result.Imported++
	}

	return result, nil
}
