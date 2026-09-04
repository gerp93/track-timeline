package database

import (
	"errors"
	"log"

	"github.com/google/uuid"
)

// UngenredCard is a song with no usable genre (null category, or a category
// id that no longer exists in TRACK_TIMELINE_CATEGORY).
type UngenredCard struct {
	CardId         uuid.UUID
	Title          string
	Artist         string
	DeckId         uuid.UUID
	DeckName       string
	YouTubeVideoId string
	ReleaseYear    int // 0 when NULL
}

const ungenredWhere = `
	C.CATEGORY_ID IS NULL
	OR NOT EXISTS (
		SELECT 1 FROM TRACK_TIMELINE_CATEGORY TC WHERE TC.ID = C.CATEGORY_ID
	)
`

// CountUngenredCards is how many songs need a genre assigned.
func CountUngenredCards() (int, error) {
	rows, err := query(`
		SELECT COUNT(*)
		FROM CARD AS C
		WHERE ` + ungenredWhere)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		if err := rows.Scan(&count); err != nil {
			log.Println(err)
			return 0, errors.New("failed to scan row in query results")
		}
	}
	return count, nil
}

// CountUngenredCardsMatching is CountUngenredCards filtered by search.
func CountUngenredCardsMatching(search string) (int, error) {
	search = "%" + search + "%"
	rows, err := query(`
		SELECT COUNT(*)
		FROM CARD AS C
			JOIN DECK AS D ON D.ID = C.DECK_ID
		WHERE (`+ungenredWhere+`)
			AND (C.TITLE LIKE ? OR C.ARTIST LIKE ? OR D.NAME LIKE ?)
	`, search, search, search)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		if err := rows.Scan(&count); err != nil {
			log.Println(err)
			return 0, errors.New("failed to scan row in query results")
		}
	}
	return count, nil
}

// SearchUngenredCards returns one page of songs missing a valid genre.
func SearchUngenredCards(search string, page int) ([]UngenredCard, error) {
	search = "%" + search + "%"
	if page < 1 {
		page = 1
	}

	rows, err := query(`
		SELECT C.ID, C.TITLE, C.ARTIST, C.DECK_ID, D.NAME,
			COALESCE(C.YOUTUBE_VIDEO_ID, ''), COALESCE(C.RELEASE_YEAR, 0)
		FROM CARD AS C
			JOIN DECK AS D ON D.ID = C.DECK_ID
		WHERE (`+ungenredWhere+`)
			AND (C.TITLE LIKE ? OR C.ARTIST LIKE ? OR D.NAME LIKE ?)
		ORDER BY D.NAME, C.ARTIST, C.TITLE
		LIMIT 10 OFFSET ?
	`, search, search, search, (page-1)*10)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]UngenredCard, 0)
	for rows.Next() {
		var c UngenredCard
		if err := rows.Scan(
			&c.CardId, &c.Title, &c.Artist, &c.DeckId, &c.DeckName,
			&c.YouTubeVideoId, &c.ReleaseYear,
		); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		out = append(out, c)
	}
	return out, nil
}

// ListUngenredCardsMatching returns every ungenred song matching search
// (no page limit) — used by bulk Claude genre assignment.
func ListUngenredCardsMatching(search string) ([]UngenredCard, error) {
	search = "%" + search + "%"
	rows, err := query(`
		SELECT C.ID, C.TITLE, C.ARTIST, C.DECK_ID, D.NAME,
			COALESCE(C.YOUTUBE_VIDEO_ID, ''), COALESCE(C.RELEASE_YEAR, 0)
		FROM CARD AS C
			JOIN DECK AS D ON D.ID = C.DECK_ID
		WHERE (`+ungenredWhere+`)
			AND (C.TITLE LIKE ? OR C.ARTIST LIKE ? OR D.NAME LIKE ?)
		ORDER BY D.NAME, C.ARTIST, C.TITLE
	`, search, search, search)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]UngenredCard, 0)
	for rows.Next() {
		var c UngenredCard
		if err := rows.Scan(
			&c.CardId, &c.Title, &c.Artist, &c.DeckId, &c.DeckName,
			&c.YouTubeVideoId, &c.ReleaseYear,
		); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		out = append(out, c)
	}
	return out, nil
}

// UpdateCardCategory sets only the genre on a card.
func UpdateCardCategory(cardId uuid.UUID, categoryId uuid.UUID) error {
	return execute("UPDATE CARD SET CATEGORY_ID = ? WHERE ID = ?", categoryId, cardId)
}
