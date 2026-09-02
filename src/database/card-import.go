package database

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	Title        string `json:"title"`
	Artist       string `json:"artist"`
	Year         *int   `json:"year"`
	VideoId      string `json:"videoId"`
	StartSeconds *int   `json:"startSeconds"`
	Category     string `json:"category"`
}

// CardImportResult reports what an import actually did. Skipped counts cards
// already present in the deck; Uncategorized counts cards imported with a
// category name that does not exist here, which land with no genre rather than
// failing the whole file.
type CardImportResult struct {
	Imported      int
	Skipped       int
	Uncategorized int
}

// ParseCardImportJSON validates an uploaded file into entries. Every problem is
// reported against the entry's index so an author can find it in the file.
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

		if entries[i].Title == "" {
			return nil, fmt.Errorf("card %d has no title", i+1)
		}
		if entries[i].Artist == "" {
			return nil, fmt.Errorf("card %d has no artist", i+1)
		}

		videoId, err := ParseYouTubeVideoId(entries[i].VideoId)
		if err != nil {
			return nil, fmt.Errorf("card %d (%s): %w", i+1, entries[i].Title, err)
		}
		entries[i].VideoId = videoId

		if entries[i].StartSeconds != nil && *entries[i].StartSeconds < 0 {
			return nil, fmt.Errorf("card %d (%s) has a negative start offset", i+1, entries[i].Title)
		}
	}

	return entries, nil
}

// ImportCardsIntoDeck inserts entries that are not already in the deck, matching
// categories by name. A card whose category name is unknown is imported without
// one rather than rejected: the alternative is failing an otherwise good file
// over a genre label.
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

	for _, entry := range entries {
		existingId, err := GetCardIdByVideo(deckId, entry.VideoId)
		if err != nil {
			return result, err
		}
		if existingId != uuid.Nil {
			result.Skipped++
			continue
		}

		var releaseYear sql.NullInt64
		if entry.Year != nil {
			releaseYear = sql.NullInt64{Int64: int64(*entry.Year), Valid: true}
		}

		startSeconds := 0
		if entry.StartSeconds != nil {
			startSeconds = *entry.StartSeconds
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

		if _, err := CreateCard(deckId, entry.VideoId, startSeconds, entry.Title, entry.Artist, releaseYear, categoryId); err != nil {
			return result, err
		}
		result.Imported++
	}

	return result, nil
}
