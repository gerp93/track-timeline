package database

import (
	"errors"
	"log"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// GenreAssignLog is one Claude API genre assignment attempt.
type GenreAssignLog struct {
	Id            uuid.UUID
	CreatedOnDate time.Time
	CardId        uuid.UUID
	Title         string
	Artist        string
	DeckName      string
	GenreName     string
	Success       bool
	ErrorText     string
}

// LogGenreAssign appends one Claude genre assignment result.
func LogGenreAssign(card UngenredCard, genreName string, success bool, errText string) error {
	id, err := uuid.NewUUID()
	if err != nil {
		return err
	}
	// Rune-safe: errText[:512] would slice by byte, which can cut a
	// multi-byte character in half and garble the stored text from there on.
	if utf8.RuneCountInString(errText) > 512 {
		errText = string([]rune(errText)[:512])
	}
	return execute(`
		INSERT INTO TRACK_TIMELINE_LOG_GENRE_ASSIGN (
			ID, CARD_ID, TITLE, ARTIST, DECK_NAME, GENRE_NAME, SUCCESS, ERROR_TEXT
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, id, card.CardId, card.Title, card.Artist, card.DeckName, genreName, success, errText)
}

// CountGenreAssignLogs is how many Claude genre log rows exist.
func CountGenreAssignLogs() (int, error) {
	rows, err := query("SELECT COUNT(*) FROM TRACK_TIMELINE_LOG_GENRE_ASSIGN")
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

// CountGenreAssignLogsMatching filters the Claude genre log by search.
func CountGenreAssignLogsMatching(search string) (int, error) {
	search = "%" + search + "%"
	rows, err := query(`
		SELECT COUNT(*)
		FROM TRACK_TIMELINE_LOG_GENRE_ASSIGN
		WHERE TITLE LIKE ? OR ARTIST LIKE ? OR DECK_NAME LIKE ? OR GENRE_NAME LIKE ?
	`, search, search, search, search)
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

// SearchGenreAssignLogs returns one page of Claude genre assignment log rows,
// newest first.
func SearchGenreAssignLogs(search string, page int) ([]GenreAssignLog, error) {
	search = "%" + search + "%"
	if page < 1 {
		page = 1
	}

	rows, err := query(`
		SELECT ID, CREATED_ON_DATE, CARD_ID, TITLE, ARTIST, DECK_NAME,
			GENRE_NAME, SUCCESS, ERROR_TEXT
		FROM TRACK_TIMELINE_LOG_GENRE_ASSIGN
		WHERE TITLE LIKE ? OR ARTIST LIKE ? OR DECK_NAME LIKE ? OR GENRE_NAME LIKE ?
		ORDER BY CREATED_ON_DATE DESC
		LIMIT 10 OFFSET ?
	`, search, search, search, search, (page-1)*10)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]GenreAssignLog, 0)
	for rows.Next() {
		var row GenreAssignLog
		if err := rows.Scan(
			&row.Id, &row.CreatedOnDate, &row.CardId, &row.Title, &row.Artist,
			&row.DeckName, &row.GenreName, &row.Success, &row.ErrorText,
		); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		out = append(out, row)
	}
	return out, nil
}

// DeckName returns the deck's display name, or "" if missing.
func DeckName(deckId uuid.UUID) (string, error) {
	rows, err := query("SELECT NAME FROM DECK WHERE ID = ?", deckId)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	if !rows.Next() {
		return "", nil
	}
	var name string
	if err := rows.Scan(&name); err != nil {
		log.Println(err)
		return "", errors.New("failed to scan row in query results")
	}
	return name, nil
}
