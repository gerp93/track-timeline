package database

import (
	"errors"
	"log"

	"github.com/google/uuid"
)

// Card lifecycle events recorded in TRACK_TIMELINE_LOG_CARD.
const (
	CardEventDrawn     = "drawn"
	CardEventDiscarded = "discarded"
	CardEventSkipped   = "skipped"
	CardEventBought    = "bought"
)

// The log tables have no foreign keys on purpose (see their DDL): a lobby and
// everything hanging off it is deleted when the last websocket client
// disconnects, and stats have to outlive that. These writers are called for
// their side effect only — a logging failure is reported to the caller so it
// can be logged, but never fails a round.

// LogPlacement records one judged placement attempt.
func LogPlacement(userId uuid.UUID, cardId uuid.UUID, releaseYear int, isChallenge bool, isCorrect bool) error {
	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return errors.New("failed to generate new id")
	}

	sqlString := `
		INSERT INTO TRACK_TIMELINE_LOG_PLACEMENT (ID, USER_ID, CARD_ID, RELEASE_YEAR, IS_CHALLENGE, IS_CORRECT)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	return execute(sqlString, id, userId, cardId, releaseYear, isChallenge, isCorrect)
}

// LogTitleGuess records one artist/title guess and its verdict. The raw text is
// kept so a judge implementation can be evaluated later against what people
// actually typed.
func LogTitleGuess(userId uuid.UUID, cardId uuid.UUID, guessText string, titleCorrect bool, artistCorrect bool) error {
	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return errors.New("failed to generate new id")
	}

	sqlString := `
		INSERT INTO TRACK_TIMELINE_LOG_TITLE_GUESS (ID, USER_ID, CARD_ID, GUESS_TEXT, TITLE_CORRECT, ARTIST_CORRECT)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	return execute(sqlString, id, userId, cardId, guessText, titleCorrect, artistCorrect)
}

// LogCardEvent records a card being drawn, discarded, or skipped.
func LogCardEvent(cardId uuid.UUID, eventType string) error {
	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return errors.New("failed to generate new id")
	}

	sqlString := "INSERT INTO TRACK_TIMELINE_LOG_CARD (ID, CARD_ID, EVENT_TYPE) VALUES (?, ?, ?)"
	return execute(sqlString, id, cardId, eventType)
}

// LogWin records a completed game.
func LogWin(userId uuid.UUID, cardsToWin int, playerCount int) error {
	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return errors.New("failed to generate new id")
	}

	sqlString := "INSERT INTO TRACK_TIMELINE_LOG_WIN (ID, USER_ID, CARDS_TO_WIN, PLAYER_COUNT) VALUES (?, ?, ?, ?)"
	return execute(sqlString, id, userId, cardsToWin, playerCount)
}
