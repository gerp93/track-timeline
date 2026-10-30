package database

import (
	"database/sql"
	"errors"
	"log"

	"github.com/google/uuid"
)

// Moments recorded in TRACK_TIMELINE_LOG_ACHIEVEMENT_EVENT. Most are a count
// (VALUE 1 each); the comments say where VALUE means something else.
const (
	EvStolenFrom        = "stolen_from"        // a steal took the card from this player
	EvStealDefended     = "steal_defended"     // a steal was made against this player's correct placement and failed
	EvDoubleDip         = "double_dip"         // placed correctly and named both the title and the artist
	EvSpellingForgiven  = "spelling_forgiven"  // a guess was credited though it was misspelled
	EvTripleCrown       = "triple_crown"       // exact year, title and artist on one song
	EvAllIn             = "all_in"             // wagered the whole stash and hit
	EvRockBottom        = "rock_bottom"        // wagered the whole stash and missed
	EvNeedle            = "needle"             // a correct placement in a gap of a year or less
	EvSoClose           = "so_close"           // a placement missed by exactly one year
	EvPeakTokens        = "peak_tokens"        // VALUE: a balance at the end of a round, logged on each new peak of a game
	EvFlatBroke         = "flat_broke"         // went from a big stash to nothing in one game
	EvNothingToLose     = "nothing_to_lose"    // claimed a steal with exactly the tokens it cost
	EvWinSpan           = "win_span"           // VALUE: years from the oldest to the newest card of a winning timeline
	EvWinSeconds        = "win_seconds"        // VALUE: seconds from start to win, for a standard-length game
	EvFlawless          = "flawless"           // won without missing an own-turn placement
	EvComeback          = "comeback"           // won after trailing the leader by ComebackDeficit
	EvDeficit           = "deficit"            // VALUE: how far behind the leader at the end of a round (logged once a game, when it reaches ComebackDeficit)
	EvPhotoFinish       = "photo_finish"       // won while a rival was one card away
	EvHeist             = "heist"              // won by stealing the final card
	EvFortKnox          = "fort_knox"          // won, having fended off steals and lost nothing to one
	EvChallengeRaised   = "challenge_raised"   // raised a challenge
	EvChallengeUpheld   = "challenge_upheld"   // had a challenge upheld
	EvChallengeRejected = "challenge_rejected" // had a challenge rejected
	EvChallengeVote     = "challenge_vote"     // voted on someone else's challenge
	EvLobbyHosted       = "lobby_hosted"       // created a lobby
)

// LogAchievementEvent records one moment for a player. gameId is uuid.Nil for a
// moment that is not part of a game. Like every log writer here it is called for
// its side effect: a failure is returned for the caller to log and never stops
// the game.
func LogAchievementEvent(userId uuid.UUID, gameId uuid.UUID, eventType string, value int) error {
	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return errors.New("failed to generate new id")
	}

	game := uuid.NullUUID{UUID: gameId, Valid: gameId != uuid.Nil}
	sqlString := `
		INSERT INTO TRACK_TIMELINE_LOG_ACHIEVEMENT_EVENT (ID, USER_ID, GAME_ID, EVENT_TYPE, VALUE)
		VALUES (?, ?, ?, ?, ?)
	`
	return execute(sqlString, id, userId, game, eventType, value)
}

// A lobby plays game after game on one game row (ResetGame clears it for a
// rematch), so "in this game" means since STARTED_ON_DATE, not merely under this
// GAME_ID: what happened in the last game must not count in this one. A game
// started before the start was recorded has no such bound, and every event
// counts.

// CountGameEvents is how many times an event was recorded for a player in the
// game as it is now being played.
func CountGameEvents(userId uuid.UUID, gameId uuid.UUID, eventType string) (int, error) {
	rows, err := query(`
		SELECT COUNT(E.ID)
		FROM TRACK_TIMELINE_GAME G
			INNER JOIN TRACK_TIMELINE_LOG_ACHIEVEMENT_EVENT E
				ON E.GAME_ID = G.ID AND E.USER_ID = ? AND E.EVENT_TYPE = ?
				AND (G.STARTED_ON_DATE IS NULL OR E.CREATED_ON_DATE >= G.STARTED_ON_DATE)
		WHERE G.ID = ?
	`, userId, eventType, gameId)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var n int
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			log.Println(err)
			return 0, errors.New("failed to scan row in query results")
		}
	}
	return n, nil
}

// MaxGameEventValue is the biggest VALUE an event was recorded with for a player
// in the game as it is now being played, or 0 when there is none.
func MaxGameEventValue(userId uuid.UUID, gameId uuid.UUID, eventType string) (int, error) {
	rows, err := query(`
		SELECT COALESCE(MAX(E.VALUE), 0)
		FROM TRACK_TIMELINE_GAME G
			INNER JOIN TRACK_TIMELINE_LOG_ACHIEVEMENT_EVENT E
				ON E.GAME_ID = G.ID AND E.USER_ID = ? AND E.EVENT_TYPE = ?
				AND (G.STARTED_ON_DATE IS NULL OR E.CREATED_ON_DATE >= G.STARTED_ON_DATE)
		WHERE G.ID = ?
	`, userId, eventType, gameId)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var n int
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			log.Println(err)
			return 0, errors.New("failed to scan row in query results")
		}
	}
	return n, nil
}

// GameSeconds is how long a game has been running, from the moment it was
// started. ok is false for a game started before that moment was recorded.
func GameSeconds(gameId uuid.UUID) (seconds int, ok bool, err error) {
	rows, err := query(`
		SELECT TIMESTAMPDIFF(SECOND, STARTED_ON_DATE, NOW())
		FROM TRACK_TIMELINE_GAME
		WHERE ID = ? AND STARTED_ON_DATE IS NOT NULL
	`, gameId)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()

	if !rows.Next() {
		return 0, false, nil
	}
	var value sql.NullInt64
	if err := rows.Scan(&value); err != nil {
		log.Println(err)
		return 0, false, errors.New("failed to scan row in query results")
	}
	return int(value.Int64), value.Valid, nil
}

// GameOwnTurnMisses is how many own-turn placements a user has missed since the
// game was started (a missed steal is not one). ok is false for a game started
// before the start was recorded, or one that does not exist: its misses cannot be
// told apart from an earlier game's, so it must not read as "none".
func GameOwnTurnMisses(userId uuid.UUID, gameId uuid.UUID) (misses int, ok bool, err error) {
	// An aggregate returns a row even when nothing matched, so whether the game
	// has a start is counted alongside rather than inferred from there being a row.
	rows, err := query(`
		SELECT COUNT(P.ID), COUNT(G.STARTED_ON_DATE)
		FROM TRACK_TIMELINE_GAME G
			LEFT JOIN TRACK_TIMELINE_LOG_PLACEMENT P
				ON P.USER_ID = ? AND P.IS_CHALLENGE = 0 AND P.IS_CORRECT = 0
				AND P.CREATED_ON_DATE >= G.STARTED_ON_DATE
		WHERE G.ID = ?
	`, userId, gameId)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()

	if !rows.Next() {
		return 0, false, nil
	}
	var started int
	if err := rows.Scan(&misses, &started); err != nil {
		log.Println(err)
		return 0, false, errors.New("failed to scan row in query results")
	}
	return misses, started > 0, nil
}
