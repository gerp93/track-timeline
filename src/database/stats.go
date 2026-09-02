package database

import (
	"database/sql"
	"errors"
	"log"

	"github.com/google/uuid"
)

// LeaderboardRow is one user's standing, ranked by wins. Rank is computed
// here (1-based position in the result) rather than in the template, since
// html/template has no arithmetic and the row order is already meaningful.
type LeaderboardRow struct {
	Rank          int
	UserId        uuid.UUID
	UserName      string
	Wins          int
	Placements    int
	CorrectPlaces int
}

// GetLeaderboard ranks every user who has ever won by win count, then by
// placement accuracy as the tiebreak. A user who has placed cards but never
// won does not appear — the leaderboard is about winning, not participation.
func GetLeaderboard() ([]LeaderboardRow, error) {
	sqlString := `
		SELECT
			W.USER_ID,
			COUNT(*) AS WINS
		FROM TRACK_TIMELINE_LOG_WIN W
		GROUP BY W.USER_ID
		ORDER BY WINS DESC
		LIMIT 50
	`
	rows, err := query(sqlString)
	if err != nil {
		return nil, err
	}

	type winRow struct {
		userId uuid.UUID
		wins   int
	}
	winRows := make([]winRow, 0)
	for rows.Next() {
		var wr winRow
		if err := rows.Scan(&wr.userId, &wr.wins); err != nil {
			rows.Close()
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		winRows = append(winRows, wr)
	}
	rows.Close()

	result := make([]LeaderboardRow, 0, len(winRows))
	for _, wr := range winRows {
		name, err := userName(wr.userId)
		if err != nil {
			name = "(unknown)"
		}
		placements, correct, err := placementAccuracy(wr.userId, false)
		if err != nil {
			placements, correct = 0, 0
		}
		result = append(result, LeaderboardRow{
			Rank:          len(result) + 1,
			UserId:        wr.userId,
			UserName:      name,
			Wins:          wr.wins,
			Placements:    placements,
			CorrectPlaces: correct,
		})
	}

	return result, nil
}

// UserStatsRow is one user's overall record, for the user list.
type UserStatsRow struct {
	UserId         uuid.UUID
	UserName       string
	Wins           int
	Placements     int
	CorrectPlaces  int
	Guesses        int
	CorrectGuesses int
}

// GetUserStatsList returns every user who has placed at least one card or made
// at least one guess, so a player who has only ever watched still doesn't
// clutter the list.
func GetUserStatsList() ([]UserStatsRow, error) {
	sqlString := `
		SELECT ID, NAME FROM USER
		WHERE ID IN (SELECT DISTINCT USER_ID FROM TRACK_TIMELINE_LOG_PLACEMENT)
			OR ID IN (SELECT DISTINCT USER_ID FROM TRACK_TIMELINE_LOG_TITLE_GUESS)
		ORDER BY NAME ASC
	`
	rows, err := query(sqlString)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type idName struct {
		id   uuid.UUID
		name string
	}
	users := make([]idName, 0)
	for rows.Next() {
		var u idName
		if err := rows.Scan(&u.id, &u.name); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		users = append(users, u)
	}

	result := make([]UserStatsRow, 0, len(users))
	for _, u := range users {
		row, err := GetUserStats(u.id)
		if err != nil {
			continue
		}
		result = append(result, row)
	}

	return result, nil
}

// GetUserStats returns one user's full record.
func GetUserStats(userId uuid.UUID) (UserStatsRow, error) {
	var row UserStatsRow
	row.UserId = userId

	name, err := userName(userId)
	if err != nil {
		return row, err
	}
	row.UserName = name

	wins, err := winCount(userId)
	if err != nil {
		return row, err
	}
	row.Wins = wins

	placements, correct, err := placementAccuracy(userId, false)
	if err != nil {
		return row, err
	}
	row.Placements = placements
	row.CorrectPlaces = correct

	guesses, correctGuesses, err := guessAccuracy(userId)
	if err != nil {
		return row, err
	}
	row.Guesses = guesses
	row.CorrectGuesses = correctGuesses

	return row, nil
}

// CardStatsRow is one song's placement record, for the cards list.
type CardStatsRow struct {
	CardId        uuid.UUID
	Title         string
	Artist        string
	ReleaseYear   int
	Attempts      int
	CorrectPlaces int
}

// GetHardestCards ranks the songs most often placed wrong, among songs
// attempted at least minAttempts times — a card only ever tried once cannot
// meaningfully be "hard", only unlucky.
func GetHardestCards(minAttempts int) ([]CardStatsRow, error) {
	sqlString := `
		SELECT
			L.CARD_ID,
			C.TITLE,
			C.ARTIST,
			L.RELEASE_YEAR,
			COUNT(*) AS ATTEMPTS,
			SUM(CASE WHEN L.IS_CORRECT THEN 1 ELSE 0 END) AS CORRECT_PLACES
		FROM TRACK_TIMELINE_LOG_PLACEMENT L
			INNER JOIN CARD C ON C.ID = L.CARD_ID
		GROUP BY L.CARD_ID, C.TITLE, C.ARTIST, L.RELEASE_YEAR
		HAVING ATTEMPTS >= ?
		ORDER BY (SUM(CASE WHEN L.IS_CORRECT THEN 1 ELSE 0 END) / COUNT(*)) ASC, ATTEMPTS DESC
		LIMIT 50
	`
	rows, err := query(sqlString, minAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]CardStatsRow, 0)
	for rows.Next() {
		var row CardStatsRow
		if err := rows.Scan(&row.CardId, &row.Title, &row.Artist, &row.ReleaseYear, &row.Attempts, &row.CorrectPlaces); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, row)
	}

	return result, nil
}

// GetCardStats returns one song's full placement record, joined against the
// live CARD row where it still exists (deleted cards keep their log rows, per
// the log tables' whole reason for being FK-free, but have no title to show).
func GetCardStats(cardId uuid.UUID) (CardStatsRow, error) {
	var row CardStatsRow
	row.CardId = cardId

	sqlString := `
		SELECT
			L.RELEASE_YEAR,
			COUNT(*) AS ATTEMPTS,
			SUM(CASE WHEN L.IS_CORRECT THEN 1 ELSE 0 END) AS CORRECT_PLACES
		FROM TRACK_TIMELINE_LOG_PLACEMENT L
		WHERE L.CARD_ID = ?
		GROUP BY L.RELEASE_YEAR
	`
	rows, err := query(sqlString, cardId)
	if err != nil {
		return row, err
	}
	for rows.Next() {
		if err := rows.Scan(&row.ReleaseYear, &row.Attempts, &row.CorrectPlaces); err != nil {
			rows.Close()
			log.Println(err)
			return row, errors.New("failed to scan row in query results")
		}
	}
	rows.Close()

	card, err := GetCard(cardId)
	if err == nil && card.Id != uuid.Nil {
		row.Title = card.Title
		row.Artist = card.Artist
	} else {
		row.Title = "(deleted song)"
	}

	return row, nil
}

// --- shared helpers ---

func userName(userId uuid.UUID) (string, error) {
	rows, err := query("SELECT NAME FROM USER WHERE ID = ?", userId)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var name string
	for rows.Next() {
		if err := rows.Scan(&name); err != nil {
			log.Println(err)
			return "", errors.New("failed to scan row in query results")
		}
	}
	return name, nil
}

func winCount(userId uuid.UUID) (int, error) {
	rows, err := query("SELECT COUNT(*) FROM TRACK_TIMELINE_LOG_WIN WHERE USER_ID = ?", userId)
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

// placementAccuracy returns (attempts, correct) for a user. When
// challengesOnly is false it counts every placement; the flag exists so a
// future page can split out challenge performance from ordinary turns without
// a second query shape.
func placementAccuracy(userId uuid.UUID, challengesOnly bool) (int, int, error) {
	sqlString := `
		SELECT COUNT(*), SUM(CASE WHEN IS_CORRECT THEN 1 ELSE 0 END)
		FROM TRACK_TIMELINE_LOG_PLACEMENT
		WHERE USER_ID = ?
	`
	args := []any{userId}
	if challengesOnly {
		sqlString += " AND IS_CHALLENGE = 1"
	}

	rows, err := query(sqlString, args...)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	var attempts int
	var correct sql.NullInt64
	for rows.Next() {
		if err := rows.Scan(&attempts, &correct); err != nil {
			log.Println(err)
			return 0, 0, errors.New("failed to scan row in query results")
		}
	}
	return attempts, int(correct.Int64), nil
}

func guessAccuracy(userId uuid.UUID) (int, int, error) {
	sqlString := `
		SELECT COUNT(*), SUM(CASE WHEN TITLE_CORRECT AND ARTIST_CORRECT THEN 1 ELSE 0 END)
		FROM TRACK_TIMELINE_LOG_TITLE_GUESS
		WHERE USER_ID = ?
	`
	rows, err := query(sqlString, userId)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	var guesses int
	var correct sql.NullInt64
	for rows.Next() {
		if err := rows.Scan(&guesses, &correct); err != nil {
			log.Println(err)
			return 0, 0, errors.New("failed to scan row in query results")
		}
	}
	return guesses, int(correct.Int64), nil
}
