package database

import (
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
)

// CardVideoId is one card's video id, for walking a deck to check every
// video without pulling the rest of the card.
type CardVideoId struct {
	CardId         uuid.UUID
	YouTubeVideoId string
}

// GetCardVideoIdsInDeck returns every card's video id in a deck.
func GetCardVideoIdsInDeck(deckId uuid.UUID) ([]CardVideoId, error) {
	sqlString := `
		SELECT ID, YOUTUBE_VIDEO_ID
		FROM CARD
		WHERE DECK_ID = ?
			AND YOUTUBE_VIDEO_ID IS NOT NULL
	`
	rows, err := query(sqlString, deckId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]CardVideoId, 0)
	for rows.Next() {
		cv, err := scanCardVideoId(rows)
		if err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, cv)
	}

	return result, nil
}

// GetAllCardVideoIds returns every card's video id across every deck, for
// checking every song's video in one pass.
func GetAllCardVideoIds() ([]CardVideoId, error) {
	sqlString := `
		SELECT ID, YOUTUBE_VIDEO_ID
		FROM CARD
		WHERE YOUTUBE_VIDEO_ID IS NOT NULL
	`
	rows, err := query(sqlString)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]CardVideoId, 0)
	for rows.Next() {
		cv, err := scanCardVideoId(rows)
		if err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, cv)
	}

	return result, nil
}

// scanCardVideoId reads a card id + nullable YouTube id (empty when NULL).
func scanCardVideoId(rows *sql.Rows) (CardVideoId, error) {
	var cv CardVideoId
	var yt sql.NullString
	if err := rows.Scan(&cv.CardId, &yt); err != nil {
		return cv, err
	}
	cv.YouTubeVideoId = yt.String
	return cv, nil
}

// GetDrawPileCardVideoIds returns the video id of every card still in a
// game's draw pile, for the StartGame freshness check. Scoped to the pile
// (already filtered by deck/year-range/genre) rather than the raw decks, so
// it never checks a song that couldn't be drawn anyway.
func GetDrawPileCardVideoIds(gameId uuid.UUID) ([]CardVideoId, error) {
	sqlString := `
		SELECT C.ID, C.YOUTUBE_VIDEO_ID
		FROM TRACK_TIMELINE_DRAW_PILE DP
			JOIN CARD C ON C.ID = DP.CARD_ID
		WHERE DP.TRACK_TIMELINE_GAME_ID = ?
			AND DP.DRAWN = 0
			AND C.YOUTUBE_VIDEO_ID IS NOT NULL
	`
	rows, err := query(sqlString, gameId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]CardVideoId, 0)
	for rows.Next() {
		cv, err := scanCardVideoId(rows)
		if err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, cv)
	}

	return result, nil
}

// HasStaleVideoChecksForCards is HasStaleVideoChecks scoped to a specific set
// of cards, for the StartGame freshness check.
//
// A playable card with no recorded duration counts as stale even if it was
// checked seconds ago: duration is what the 'sample' playback mode needs to
// pick a window that fits, and it is only ever populated by a check, so
// without this a database whose cards were all checked before durations were
// recorded would never backfill them. Scoped to AVAILABLE = 1 because an
// unavailable card is excluded from the pile anyway and re-checking it for a
// length nobody will use is wasted quota.
func HasStaleVideoChecksForCards(cardIds []uuid.UUID, olderThan time.Time) (bool, error) {
	if len(cardIds) == 0 {
		return false, nil
	}

	placeholders := make([]any, 0, len(cardIds)+1)
	inClause := ""
	for i, id := range cardIds {
		placeholders = append(placeholders, id)
		if i > 0 {
			inClause += ", "
		}
		inClause += "?"
	}
	placeholders = append(placeholders, olderThan)

	sqlString := `
		SELECT COUNT(*)
		FROM CARD AS C
			LEFT JOIN TRACK_TIMELINE_CARD_VIDEO_STATUS AS S ON S.CARD_ID = C.ID
		WHERE C.ID IN (` + inClause + `)
			AND C.YOUTUBE_VIDEO_ID IS NOT NULL
			AND (
				S.CARD_ID IS NULL
				OR S.CHECKED_ON_DATE < ?
				OR (S.AVAILABLE = 1 AND S.DURATION_SECONDS IS NULL)
			)
	`
	rows, err := query(sqlString, placeholders...)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		if err := rows.Scan(&count); err != nil {
			log.Println(err)
			return false, errors.New("failed to scan row in query results")
		}
	}

	return count > 0, nil
}

// PruneUnavailableFromDrawPile removes any undrawn pile card now confirmed
// unavailable, mirroring ApplyYearRangeFilter's prune-after-build pattern.
// Called after a StartGame freshness check might have found something newly
// broken since the pile was originally built at lobby Create time. Re-shuffles
// whatever remains so holes left by the prune do not bias later draws.
func PruneUnavailableFromDrawPile(gameId uuid.UUID) error {
	sqlString := `
		DELETE FROM TRACK_TIMELINE_DRAW_PILE
		WHERE TRACK_TIMELINE_GAME_ID = ?
			AND DRAWN = 0
			AND CARD_ID IN (
				SELECT CARD_ID FROM TRACK_TIMELINE_CARD_VIDEO_STATUS
				WHERE AVAILABLE = 0 OR AWAITING_VALIDATION = 1
			)
	`
	if err := execute(sqlString, gameId); err != nil {
		return err
	}
	return ShuffleDrawPile(gameId)
}

// staleVideoWhere is shared by count/list of cards that need a fresh
// videos.list pass: never checked, last checked before olderThan, or
// playable with no duration yet.
const staleVideoWhere = `
	C.YOUTUBE_VIDEO_ID IS NOT NULL
	AND (
		S.CARD_ID IS NULL
		OR S.CHECKED_ON_DATE < ?
		OR (S.AVAILABLE = 1 AND S.DURATION_SECONDS IS NULL)
	)
`

// CountStaleVideoChecks returns how many cards need a fresh YouTube check.
func CountStaleVideoChecks(olderThan time.Time) (int, error) {
	sqlString := `
		SELECT COUNT(*)
		FROM CARD AS C
			LEFT JOIN TRACK_TIMELINE_CARD_VIDEO_STATUS AS S ON S.CARD_ID = C.ID
		WHERE ` + staleVideoWhere
	rows, err := query(sqlString, olderThan)
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

// GetStaleCardVideoIds returns every card that CountStaleVideoChecks would
// include, for the admin stale-check button on the Dead Videos page.
func GetStaleCardVideoIds(olderThan time.Time) ([]CardVideoId, error) {
	sqlString := `
		SELECT C.ID, C.YOUTUBE_VIDEO_ID
		FROM CARD AS C
			LEFT JOIN TRACK_TIMELINE_CARD_VIDEO_STATUS AS S ON S.CARD_ID = C.ID
		WHERE ` + staleVideoWhere
	rows, err := query(sqlString, olderThan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]CardVideoId, 0)
	for rows.Next() {
		cv, err := scanCardVideoId(rows)
		if err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, cv)
	}
	return result, nil
}

// HasStaleVideoChecks reports whether any card needs a fresh check.
func HasStaleVideoChecks(olderThan time.Time) (bool, error) {
	count, err := CountStaleVideoChecks(olderThan)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// SetVideoStatus records the result of checking a card's video.
// durationSeconds of 0 is stored as NULL (unknown): YouTube reports live
// streams and unstarted premieres as zero-length, and a card we could not
// measure must not look like a card that is genuinely zero seconds long.
// Always clears AWAITING_VALIDATION and INCORRECT_VIDEO — a completed check
// resolves those states.
func SetVideoStatus(cardId uuid.UUID, available bool, durationSeconds int) error {
	var duration any
	if durationSeconds > 0 {
		duration = durationSeconds
	}

	sqlString := `
		INSERT INTO TRACK_TIMELINE_CARD_VIDEO_STATUS(
			CARD_ID, AVAILABLE, DURATION_SECONDS, AWAITING_VALIDATION, INCORRECT_VIDEO
		)
		VALUES (?, ?, ?, 0, 0)
		ON DUPLICATE KEY UPDATE
			AVAILABLE = VALUES(AVAILABLE),
			DURATION_SECONDS = VALUES(DURATION_SECONDS),
			AWAITING_VALIDATION = 0,
			INCORRECT_VIDEO = 0,
			CHECKED_ON_DATE = CURRENT_TIMESTAMP()
	`
	return execute(sqlString, cardId, available, duration)
}

// MarkVideoAwaitingValidation flags a card whose YouTube link was just
// changed and has not been re-checked yet. Clears INCORRECT_VIDEO so a
// replaced link is not stuck as "wrong song".
func MarkVideoAwaitingValidation(cardId uuid.UUID) error {
	sqlString := `
		INSERT INTO TRACK_TIMELINE_CARD_VIDEO_STATUS(
			CARD_ID, AVAILABLE, DURATION_SECONDS, AWAITING_VALIDATION, INCORRECT_VIDEO
		)
		VALUES (?, 0, NULL, 1, 0)
		ON DUPLICATE KEY UPDATE
			AWAITING_VALIDATION = 1,
			INCORRECT_VIDEO = 0
	`
	return execute(sqlString, cardId)
}

// MarkVideoOk marks a card playable without calling YouTube. Clears awaiting
// and incorrect flags; leaves any known duration alone.
func MarkVideoOk(cardId uuid.UUID) error {
	sqlString := `
		INSERT INTO TRACK_TIMELINE_CARD_VIDEO_STATUS(
			CARD_ID, AVAILABLE, DURATION_SECONDS, AWAITING_VALIDATION, INCORRECT_VIDEO
		)
		VALUES (?, 1, NULL, 0, 0)
		ON DUPLICATE KEY UPDATE
			AVAILABLE = 1,
			AWAITING_VALIDATION = 0,
			INCORRECT_VIDEO = 0,
			CHECKED_ON_DATE = CURRENT_TIMESTAMP()
	`
	return execute(sqlString, cardId)
}

// MarkVideoUnavailable sets unavailable and clears awaiting / incorrect.
func MarkVideoUnavailable(cardId uuid.UUID) error {
	sqlString := `
		INSERT INTO TRACK_TIMELINE_CARD_VIDEO_STATUS(
			CARD_ID, AVAILABLE, DURATION_SECONDS, AWAITING_VALIDATION, INCORRECT_VIDEO
		)
		VALUES (?, 0, NULL, 0, 0)
		ON DUPLICATE KEY UPDATE
			AVAILABLE = 0,
			AWAITING_VALIDATION = 0,
			INCORRECT_VIDEO = 0,
			CHECKED_ON_DATE = CURRENT_TIMESTAMP()
	`
	return execute(sqlString, cardId)
}

// MarkVideoIncorrect flags the linked video as the wrong song.
func MarkVideoIncorrect(cardId uuid.UUID) error {
	sqlString := `
		INSERT INTO TRACK_TIMELINE_CARD_VIDEO_STATUS(
			CARD_ID, AVAILABLE, DURATION_SECONDS, AWAITING_VALIDATION, INCORRECT_VIDEO
		)
		VALUES (?, 0, NULL, 0, 1)
		ON DUPLICATE KEY UPDATE
			AVAILABLE = 0,
			AWAITING_VALIDATION = 0,
			INCORRECT_VIDEO = 1,
			CHECKED_ON_DATE = CURRENT_TIMESTAMP()
	`
	return execute(sqlString, cardId)
}

// CountUnavailableVideosInDeck reports how many cards in a deck were last
// checked and found unavailable. Cards never checked don't count.
func CountUnavailableVideosInDeck(deckId uuid.UUID) (int, error) {
	sqlString := `
		SELECT COUNT(*)
		FROM CARD AS C
			JOIN TRACK_TIMELINE_CARD_VIDEO_STATUS AS S ON S.CARD_ID = C.ID
		WHERE C.DECK_ID = ?
			AND S.AVAILABLE = 0
	`
	rows, err := query(sqlString, deckId)
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

// DeadVideoCard is one row on the admin dead-video maintenance page.
type DeadVideoCard struct {
	CardId             uuid.UUID
	Title              string
	Artist             string
	DeckId             uuid.UUID
	DeckName           string
	YouTubeVideoId     string
	AwaitingValidation bool
	IncorrectVideo     bool
	CheckedOnDate      sql.NullTime
}

func deadVideoStatusWhere(status string) string {
	switch status {
	case "unavailable":
		return "S.AVAILABLE = 0 AND S.AWAITING_VALIDATION = 0 AND S.INCORRECT_VIDEO = 0"
	case "awaiting":
		return "S.AWAITING_VALIDATION = 1"
	case "incorrect":
		return "S.INCORRECT_VIDEO = 1"
	default:
		return "S.AVAILABLE = 0 OR S.AWAITING_VALIDATION = 1 OR S.INCORRECT_VIDEO = 1"
	}
}

// CountDeadOrAwaitingVideos counts cards matching the requested dead-video
// status and search text.
func CountDeadOrAwaitingVideos(search string, status string) (int, error) {
	search = "%" + search + "%"
	sqlString := `
		SELECT COUNT(*)
		FROM CARD AS C
			JOIN TRACK_TIMELINE_CARD_VIDEO_STATUS AS S ON S.CARD_ID = C.ID
			JOIN DECK AS D ON D.ID = C.DECK_ID
		WHERE (` + deadVideoStatusWhere(status) + `)
			AND (C.TITLE LIKE ? OR C.ARTIST LIKE ? OR D.NAME LIKE ?)
	`
	rows, err := query(sqlString, search, search, search)
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

// CountLibraryIssues is the admin Library badge: dead/awaiting/incorrect
// songs, undismissed duplicate groups, and songs missing a valid genre.
func CountLibraryIssues() (int, error) {
	dead, err := CountDeadOrAwaitingVideos("", "all")
	if err != nil {
		return 0, err
	}
	duplicates, err := CountDuplicateGroups()
	if err != nil {
		return 0, err
	}
	ungenred, err := CountUngenredCards()
	if err != nil {
		return 0, err
	}
	return dead + duplicates + ungenred, nil
}

// SearchDeadOrAwaitingVideos returns one page of dead / awaiting-validation
// cards, ordered by deck name then title. Never grouped by deck — deck is a
// column so admins can fix across the whole library in one list.
func SearchDeadOrAwaitingVideos(search string, page int, status string) ([]DeadVideoCard, error) {
	search = "%" + search + "%"
	if page < 1 {
		page = 1
	}

	sqlString := `
		SELECT C.ID, C.TITLE, C.ARTIST, C.DECK_ID, D.NAME, COALESCE(C.YOUTUBE_VIDEO_ID, ''),
			S.AWAITING_VALIDATION, S.INCORRECT_VIDEO, S.CHECKED_ON_DATE
		FROM CARD AS C
			JOIN TRACK_TIMELINE_CARD_VIDEO_STATUS AS S ON S.CARD_ID = C.ID
			JOIN DECK AS D ON D.ID = C.DECK_ID
		WHERE (` + deadVideoStatusWhere(status) + `)
			AND (C.TITLE LIKE ? OR C.ARTIST LIKE ? OR D.NAME LIKE ?)
		ORDER BY D.NAME, C.ARTIST, C.TITLE
		LIMIT 10 OFFSET ?
	`
	rows, err := query(sqlString, search, search, search, (page-1)*10)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]DeadVideoCard, 0)
	for rows.Next() {
		var card DeadVideoCard
		var awaiting int
		var incorrect int
		if err := rows.Scan(
			&card.CardId, &card.Title, &card.Artist, &card.DeckId, &card.DeckName,
			&card.YouTubeVideoId, &awaiting, &incorrect, &card.CheckedOnDate,
		); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		card.AwaitingValidation = awaiting != 0
		card.IncorrectVideo = incorrect != 0
		result = append(result, card)
	}
	return result, nil
}

// GetAwaitingRecheckVideoIds returns cards marked awaiting validation after a
// link edit / Find — the set Resolve re-checks.
func GetAwaitingRecheckVideoIds() ([]CardVideoId, error) {
	sqlString := `
		SELECT C.ID, C.YOUTUBE_VIDEO_ID
		FROM CARD AS C
			JOIN TRACK_TIMELINE_CARD_VIDEO_STATUS AS S ON S.CARD_ID = C.ID
		WHERE S.AWAITING_VALIDATION = 1
			AND C.YOUTUBE_VIDEO_ID IS NOT NULL
	`
	rows, err := query(sqlString)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]CardVideoId, 0)
	for rows.Next() {
		cv, err := scanCardVideoId(rows)
		if err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, cv)
	}
	return result, nil
}

// GetDeadOrAwaitingVideoIds returns CardVideoId rows for every dead or
// awaiting card, optionally limited to the given card ids (empty = all).
func GetDeadOrAwaitingVideoIds(cardIds []uuid.UUID) ([]CardVideoId, error) {
	sqlString := `
		SELECT C.ID, COALESCE(C.YOUTUBE_VIDEO_ID, '')
		FROM CARD AS C
			JOIN TRACK_TIMELINE_CARD_VIDEO_STATUS AS S ON S.CARD_ID = C.ID
		WHERE (S.AVAILABLE = 0 OR S.AWAITING_VALIDATION = 1 OR S.INCORRECT_VIDEO = 1)
	`
	args := make([]any, 0, len(cardIds))
	if len(cardIds) > 0 {
		placeholders := ""
		for i, id := range cardIds {
			if i > 0 {
				placeholders += ", "
			}
			placeholders += "?"
			args = append(args, id)
		}
		sqlString += " AND C.ID IN (" + placeholders + ")"
	}

	rows, err := query(sqlString, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]CardVideoId, 0)
	for rows.Next() {
		var cv CardVideoId
		if err := rows.Scan(&cv.CardId, &cv.YouTubeVideoId); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, cv)
	}
	return result, nil
}

// GetCardsByIdsForExport returns the metadata rows used by the dead-video
// JSON export (title, artist, deck, id).
func GetCardsByIdsForExport(cardIds []uuid.UUID) ([]DeadVideoCard, error) {
	result := make([]DeadVideoCard, 0)
	if len(cardIds) == 0 {
		return result, nil
	}

	placeholders := ""
	args := make([]any, len(cardIds))
	for i, id := range cardIds {
		if i > 0 {
			placeholders += ", "
		}
		placeholders += "?"
		args[i] = id
	}

	sqlString := `
		SELECT C.ID, C.TITLE, C.ARTIST, C.DECK_ID, D.NAME, COALESCE(C.YOUTUBE_VIDEO_ID, '')
		FROM CARD AS C
			JOIN DECK AS D ON D.ID = C.DECK_ID
		WHERE C.ID IN (` + placeholders + `)
		ORDER BY D.NAME, C.ARTIST, C.TITLE
	`
	rows, err := query(sqlString, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var card DeadVideoCard
		if err := rows.Scan(
			&card.CardId, &card.Title, &card.Artist, &card.DeckId, &card.DeckName, &card.YouTubeVideoId,
		); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, card)
	}
	return result, nil
}
