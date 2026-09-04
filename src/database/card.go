package database

import (
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Card is one song: what plays, and the answer it is played against.
type Card struct {
	Id            uuid.UUID
	CreatedOnDate time.Time
	ChangedOnDate time.Time

	DeckId         uuid.UUID
	YouTubeVideoId string
	Title          string
	Artist         string
	ReleaseYear    sql.NullInt64
	CategoryId     uuid.NullUUID
	CategoryName   sql.NullString
	// VideoAvailable is unset (Valid false) when the video has never been
	// checked. Read-only decoration from TRACK_TIMELINE_CARD_VIDEO_STATUS,
	// never written through Card itself -- see database.SetVideoStatus.
	VideoAvailable sql.NullBool
	// VideoAwaiting is true when the YouTube link was edited and has not
	// been re-checked against the API yet.
	VideoAwaiting bool
}

const cardSelectColumns = `
	C.ID,
	C.CREATED_ON_DATE,
	C.CHANGED_ON_DATE,
	C.DECK_ID,
	COALESCE(C.YOUTUBE_VIDEO_ID, ''),
	C.TITLE,
	C.ARTIST,
	C.RELEASE_YEAR,
	C.CATEGORY_ID,
	TC.NAME,
	S.AVAILABLE,
	COALESCE(S.AWAITING_VALIDATION, 0)
`

// cardFromJoin is every table a card-select query joins in, so the FROM
// clause is written once and stays in sync with cardSelectColumns/scanCard.
const cardFromJoin = `
	FROM CARD AS C
		LEFT JOIN TRACK_TIMELINE_CATEGORY AS TC ON TC.ID = C.CATEGORY_ID
		LEFT JOIN TRACK_TIMELINE_CARD_VIDEO_STATUS AS S ON S.CARD_ID = C.ID
`

func scanCard(rows *sql.Rows) (Card, error) {
	var card Card
	err := rows.Scan(
		&card.Id,
		&card.CreatedOnDate,
		&card.ChangedOnDate,
		&card.DeckId,
		&card.YouTubeVideoId,
		&card.Title,
		&card.Artist,
		&card.ReleaseYear,
		&card.CategoryId,
		&card.CategoryName,
		&card.VideoAvailable,
		&card.VideoAwaiting,
	)
	return card, err
}

// SearchCardsInDeck returns one page of cards in a deck, matching title or
// artist. Ordered by release year so an author scanning the deck sees it the
// way the game will lay it out. videoFilter is "", "available", or
// "unavailable" (never-checked cards are neither).
func SearchCardsInDeck(deckId uuid.UUID, search string, page int, videoFilter string) ([]Card, error) {
	search = "%" + search + "%"
	if page < 1 {
		page = 1
	}

	sqlString := `
		SELECT ` + cardSelectColumns + cardFromJoin + `
		WHERE C.DECK_ID = ?
			AND (C.TITLE LIKE ? OR C.ARTIST LIKE ?)
	` + videoFilterClause(videoFilter) + `
		ORDER BY C.RELEASE_YEAR, C.ARTIST, C.TITLE
		LIMIT 10 OFFSET ?
	`
	rows, err := query(sqlString, deckId, search, search, (page-1)*10)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]Card, 0)
	for rows.Next() {
		card, err := scanCard(rows)
		if err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, card)
	}

	return result, nil
}

// CountCardsInDeck counts cards in a deck matching title or artist and an
// optional videoFilter (see SearchCardsInDeck).
func CountCardsInDeck(deckId uuid.UUID, search string, videoFilter string) (int, error) {
	search = "%" + search + "%"

	sqlString := `
		SELECT COUNT(*)
		FROM CARD AS C
			LEFT JOIN TRACK_TIMELINE_CARD_VIDEO_STATUS AS S ON S.CARD_ID = C.ID
		WHERE C.DECK_ID = ?
			AND (C.TITLE LIKE ? OR C.ARTIST LIKE ?)
	` + videoFilterClause(videoFilter)
	rows, err := query(sqlString, deckId, search, search)
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

func videoFilterClause(videoFilter string) string {
	switch videoFilter {
	case "available":
		return " AND S.AVAILABLE = 1 AND S.AWAITING_VALIDATION = 0"
	case "unavailable":
		return " AND (S.AVAILABLE = 0 OR S.AWAITING_VALIDATION = 1)"
	default:
		return ""
	}
}

// GetCard returns one card. A missing card comes back as the zero Card with a
// nil error, so callers check Id against uuid.Nil.
func GetCard(cardId uuid.UUID) (Card, error) {
	var card Card

	sqlString := `
		SELECT ` + cardSelectColumns + cardFromJoin + `
		WHERE C.ID = ?
	`
	rows, err := query(sqlString, cardId)
	if err != nil {
		return card, err
	}
	defer rows.Close()

	for rows.Next() {
		card, err = scanCard(rows)
		if err != nil {
			log.Println(err)
			return card, errors.New("failed to scan row in query results")
		}
	}

	return card, nil
}

// GetCardIdByVideo finds an existing card in a deck by its video id, so create
// and import can report a duplicate rather than tripping DECK_VIDEO_UNIQUE.
// Returns uuid.Nil when there is no such card.
func GetCardIdByVideo(deckId uuid.UUID, youTubeVideoId string) (uuid.UUID, error) {
	var id uuid.UUID

	sqlString := "SELECT ID FROM CARD WHERE DECK_ID = ? AND YOUTUBE_VIDEO_ID = ?"
	rows, err := query(sqlString, deckId, youTubeVideoId)
	if err != nil {
		return id, err
	}
	defer rows.Close()

	for rows.Next() {
		if err := rows.Scan(&id); err != nil {
			log.Println(err)
			return id, errors.New("failed to scan row in query results")
		}
	}

	return id, nil
}

// CreateCard inserts a card and returns its new id.
func CreateCard(
	deckId uuid.UUID,
	youTubeVideoId string,
	title string,
	artist string,
	releaseYear sql.NullInt64,
	categoryId uuid.NullUUID,
) (uuid.UUID, error) {
	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return id, errors.New("failed to generate new id")
	}

	sqlString := `
		INSERT INTO CARD(
			ID,
			DECK_ID,
			YOUTUBE_VIDEO_ID,
			TITLE,
			ARTIST,
			RELEASE_YEAR,
			CATEGORY_ID
		)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	var videoArg any
	if youTubeVideoId != "" {
		videoArg = youTubeVideoId
	}
	return id, execute(sqlString, id, deckId, videoArg, title, artist, releaseYear, categoryId)
}

// UpdateCard rewrites every editable field of a card.
func UpdateCard(
	cardId uuid.UUID,
	youTubeVideoId string,
	title string,
	artist string,
	releaseYear sql.NullInt64,
	categoryId uuid.NullUUID,
) error {
	sqlString := `
		UPDATE CARD
		SET
			YOUTUBE_VIDEO_ID = ?,
			TITLE = ?,
			ARTIST = ?,
			RELEASE_YEAR = ?,
			CATEGORY_ID = ?
		WHERE ID = ?
	`
	return execute(sqlString, youTubeVideoId, title, artist, releaseYear, categoryId, cardId)
}

// UpdateCardYouTubeVideoId changes only the video link. Used by the admin
// dead-video repair flow and its JSON import — title/artist/year/genre must
// not be touched.
func UpdateCardYouTubeVideoId(cardId uuid.UUID, youTubeVideoId string) error {
	return execute("UPDATE CARD SET YOUTUBE_VIDEO_ID = ? WHERE ID = ?", youTubeVideoId, cardId)
}

// DeleteCard removes a card. TR_AUDIT_CARD_DELETE records it first.
func DeleteCard(cardId uuid.UUID) error {
	return execute("DELETE FROM CARD WHERE ID = ?", cardId)
}

// AuditDeckCardsAsDeleted writes an AUDIT_CARD row for every card in a deck.
// Called from the OnDeckDeleting hook because MariaDB's ON DELETE CASCADE from
// DECK to CARD does not fire CARD's own delete trigger — without this, deleting
// a deck would silently discard its cards' history.
func AuditDeckCardsAsDeleted(deckId uuid.UUID) error {
	sqlString := `
		INSERT INTO AUDIT_CARD(
			AUDIT_TYPE,
			CARD_ID,
			DECK_ID,
			YOUTUBE_VIDEO_ID,
			TITLE,
			ARTIST,
			RELEASE_YEAR,
			CATEGORY_ID
		)
		SELECT
			'DELETE',
			ID,
			DECK_ID,
			YOUTUBE_VIDEO_ID,
			TITLE,
			ARTIST,
			RELEASE_YEAR,
			CATEGORY_ID
		FROM CARD
		WHERE DECK_ID = ?
	`
	return execute(sqlString, deckId)
}

// GetCardsInDeckExport returns every card in a deck, ordered for export.
func GetCardsInDeckExport(deckId uuid.UUID) ([]Card, error) {
	sqlString := `
		SELECT ` + cardSelectColumns + cardFromJoin + `
		WHERE C.DECK_ID = ?
		ORDER BY C.RELEASE_YEAR, C.ARTIST, C.TITLE
	`
	rows, err := query(sqlString, deckId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]Card, 0)
	for rows.Next() {
		card, err := scanCard(rows)
		if err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, card)
	}

	return result, nil
}

// GetRandomCard picks one authored song at random for the admin match-test
// page. Empty Id means the library has no cards yet.
func GetRandomCard() (Card, error) {
	var card Card

	sqlString := `
		SELECT ` + cardSelectColumns + cardFromJoin + `
		ORDER BY RAND()
		LIMIT 1
	`
	rows, err := query(sqlString)
	if err != nil {
		return card, err
	}
	defer rows.Close()

	for rows.Next() {
		card, err = scanCard(rows)
		if err != nil {
			log.Println(err)
			return card, errors.New("failed to scan row in query results")
		}
	}

	return card, nil
}

// SearchCardsByName finds songs by title for the admin guess-test page.
func SearchCardsByName(search string) ([]Card, error) {
	search = strings.TrimSpace(search)
	if search == "" {
		return nil, nil
	}
	like := "%" + search + "%"

	sqlString := `
		SELECT ` + cardSelectColumns + cardFromJoin + `
		WHERE C.TITLE LIKE ?
		ORDER BY C.TITLE, C.ARTIST
		LIMIT 25
	`
	rows, err := query(sqlString, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]Card, 0)
	for rows.Next() {
		card, err := scanCard(rows)
		if err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, card)
	}

	return result, nil
}
