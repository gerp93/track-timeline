package database

import (
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
)

// Placement is one committed guess at where the song in play belongs.
type Placement struct {
	Id            uuid.UUID
	CreatedOnDate time.Time
	PlayerId      uuid.UUID
	PlayerName    string
	Position      int
	IsChallenge   bool
}

// GetPlacements returns this round's placements oldest first, which is the
// order challengers are resolved in.
func GetPlacements(gameId uuid.UUID) ([]Placement, error) {
	sqlString := `
		SELECT PL.ID, PL.CREATED_ON_DATE, PL.PLAYER_ID, U.NAME, PL.POSITION, PL.IS_CHALLENGE
		FROM TRACK_TIMELINE_PLACEMENT PL
			INNER JOIN PLAYER P ON P.ID = PL.PLAYER_ID
			INNER JOIN USER U ON U.ID = P.USER_ID
		WHERE PL.TRACK_TIMELINE_GAME_ID = ?
		ORDER BY PL.CREATED_ON_DATE ASC
	`
	rows, err := query(sqlString, gameId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]Placement, 0)
	for rows.Next() {
		var placement Placement
		if err := rows.Scan(
			&placement.Id,
			&placement.CreatedOnDate,
			&placement.PlayerId,
			&placement.PlayerName,
			&placement.Position,
			&placement.IsChallenge,
		); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, placement)
	}

	return result, nil
}

// CommitPlacement records a placement. The GAME_PLAYER_UNIQUE constraint means
// a player gets exactly one per round, so a duplicate is a conflict rather than
// an overwrite — you do not get to move your guess once it is in.
func CommitPlacement(gameId uuid.UUID, playerId uuid.UUID, position int, isChallenge bool) error {
	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return errors.New("failed to generate new id")
	}

	sqlString := `
		INSERT INTO TRACK_TIMELINE_PLACEMENT (ID, TRACK_TIMELINE_GAME_ID, PLAYER_ID, POSITION, IS_CHALLENGE)
		VALUES (?, ?, ?, ?, ?)
	`
	return execute(sqlString, id, gameId, playerId, position, isChallenge)
}

// ClearPlacements empties the round's placements.
func ClearPlacements(gameId uuid.UUID) error {
	return execute("DELETE FROM TRACK_TIMELINE_PLACEMENT WHERE TRACK_TIMELINE_GAME_ID = ?", gameId)
}

// GetPlayerTokens returns a player's balance, falling back to the game's
// starting allowance when no row exists yet.
func GetPlayerTokens(gameId uuid.UUID, playerId uuid.UUID) (int, error) {
	sqlString := `
		SELECT TOKEN_COUNT
		FROM TRACK_TIMELINE_PLAYER_TOKEN
		WHERE TRACK_TIMELINE_GAME_ID = ? AND PLAYER_ID = ?
	`
	rows, err := query(sqlString, gameId, playerId)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	if rows.Next() {
		var count int
		if err := rows.Scan(&count); err != nil {
			log.Println(err)
			return 0, errors.New("failed to scan row in query results")
		}
		return count, nil
	}

	game, err := GetGameById(gameId)
	if err != nil {
		return 0, err
	}
	return game.StartingTokens, nil
}

// SetPlayerTokens writes an absolute balance, creating the row if needed.
func SetPlayerTokens(gameId uuid.UUID, playerId uuid.UUID, count int) error {
	if count < 0 {
		count = 0
	}
	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return errors.New("failed to generate new id")
	}

	sqlString := `
		INSERT INTO TRACK_TIMELINE_PLAYER_TOKEN (ID, TRACK_TIMELINE_GAME_ID, PLAYER_ID, TOKEN_COUNT)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE TOKEN_COUNT = VALUES(TOKEN_COUNT)
	`
	return execute(sqlString, id, gameId, playerId, count)
}

// AddPlayerTokens adjusts a balance by delta and returns the new value.
func AddPlayerTokens(gameId uuid.UUID, playerId uuid.UUID, delta int) (int, error) {
	current, err := GetPlayerTokens(gameId, playerId)
	if err != nil {
		return 0, err
	}
	next := current + delta
	if next < 0 {
		next = 0
	}
	return next, SetPlayerTokens(gameId, playerId, next)
}

// HasGuessed reports whether a player already guessed this song. One guess per
// player per card is what stops someone spraying attempts until one lands.
func HasGuessed(gameId uuid.UUID, playerId uuid.UUID) (bool, error) {
	sqlString := `
		SELECT COUNT(*)
		FROM TRACK_TIMELINE_TITLE_GUESS
		WHERE TRACK_TIMELINE_GAME_ID = ? AND PLAYER_ID = ?
	`
	rows, err := query(sqlString, gameId, playerId)
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

// RecordGuess stores a judged guess.
func RecordGuess(gameId uuid.UUID, playerId uuid.UUID, guessText string, titleCorrect bool, artistCorrect bool, tokensAwarded int) error {
	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return errors.New("failed to generate new id")
	}

	sqlString := `
		INSERT INTO TRACK_TIMELINE_TITLE_GUESS
			(ID, TRACK_TIMELINE_GAME_ID, PLAYER_ID, GUESS_TEXT, TITLE_CORRECT, ARTIST_CORRECT, TOKENS_AWARDED)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	return execute(sqlString, id, gameId, playerId, guessText, titleCorrect, artistCorrect, tokensAwarded)
}

// ClearGuesses empties the round's guesses.
func ClearGuesses(gameId uuid.UUID) error {
	return execute("DELETE FROM TRACK_TIMELINE_TITLE_GUESS WHERE TRACK_TIMELINE_GAME_ID = ?", gameId)
}

// IsPlacementCorrect reports whether inserting a song of releaseYear at position
// keeps the timeline in order. A timeline is always sorted ascending, so the
// test is only against the neighbours the insert would sit between.
//
// Equal years count as correct on both sides: two songs from the same year are
// genuinely in order either way round, and failing a player for picking the
// "wrong" side of a tie would be arbitrary.
func IsPlacementCorrect(timeline []TimelineCard, position int, releaseYear int) bool {
	if position < 0 || position > len(timeline) {
		return false
	}
	if position > 0 && timeline[position-1].ReleaseYear > releaseYear {
		return false
	}
	if position < len(timeline) && timeline[position].ReleaseYear < releaseYear {
		return false
	}
	return true
}

// insertIntoTimeline shifts later cards along and writes the won card in.
func insertIntoTimeline(gameId uuid.UUID, playerId uuid.UUID, cardId uuid.UUID, releaseYear int, position int) error {
	sqlShift := `
		UPDATE TRACK_TIMELINE_PLAYER_TIMELINE
		SET POSITION = POSITION + 1
		WHERE TRACK_TIMELINE_GAME_ID = ? AND PLAYER_ID = ? AND POSITION >= ?
	`
	if err := execute(sqlShift, gameId, playerId, position); err != nil {
		return err
	}

	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return errors.New("failed to generate new id")
	}
	sqlInsert := `
		INSERT INTO TRACK_TIMELINE_PLAYER_TIMELINE
			(ID, TRACK_TIMELINE_GAME_ID, PLAYER_ID, CARD_ID, RELEASE_YEAR, POSITION)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	return execute(sqlInsert, id, gameId, playerId, cardId, releaseYear, position)
}

// RoundOutcome is what the reveal produced, for the announcement and the popup.
type RoundOutcome struct {
	CardId      uuid.UUID
	Title       string
	Artist      string
	ReleaseYear int

	// CurrentPlayerName is who was on turn, and CurrentPlayerCorrect whether
	// their own placement stood up.
	CurrentPlayerName    string
	CurrentPlayerCorrect bool

	// WinnerPlayerId is who actually took the card. Invalid when nobody did.
	WinnerPlayerId uuid.NullUUID
	WinnerName     string
	WonByChallenge bool
}

// ResolveRound reveals the answer and awards the card. The player on turn is
// judged first; only if they were wrong do challengers get their chance, in the
// order they committed. Placements and guesses are cleared either way.
//
// It does not advance the turn or draw the next song — the caller does that
// after it has also checked for a winner, so a won game stops on the reveal
// instead of flicking straight to the next round.
func ResolveRound(gameId uuid.UUID) (RoundOutcome, error) {
	var outcome RoundOutcome

	card, err := GetCurrentCardAnswer(gameId)
	if err != nil {
		return outcome, err
	}
	if card.CardId == uuid.Nil {
		return outcome, errors.New("no song is in play")
	}
	outcome.CardId = card.CardId
	outcome.Title = card.Title
	outcome.Artist = card.Artist
	outcome.ReleaseYear = card.ReleaseYear

	placements, err := GetPlacements(gameId)
	if err != nil {
		return outcome, err
	}

	game, err := GetGameById(gameId)
	if err != nil {
		return outcome, err
	}

	// The player on turn first, then challengers in commit order.
	ordered := make([]Placement, 0, len(placements))
	for _, placement := range placements {
		if !placement.IsChallenge {
			ordered = append(ordered, placement)
			outcome.CurrentPlayerName = placement.PlayerName
		}
	}
	for _, placement := range placements {
		if placement.IsChallenge {
			ordered = append(ordered, placement)
		}
	}

	awarded := false
	for _, placement := range ordered {
		timeline, err := GetPlayerTimeline(gameId, placement.PlayerId)
		if err != nil {
			return outcome, err
		}
		correct := IsPlacementCorrect(timeline, placement.Position, card.ReleaseYear)

		if !placement.IsChallenge {
			outcome.CurrentPlayerCorrect = correct
		}

		userId, err := userIdForPlayer(placement.PlayerId)
		if err == nil {
			if logErr := LogPlacement(userId, card.CardId, card.ReleaseYear, placement.IsChallenge, correct); logErr != nil {
				log.Println(logErr)
			}
		}

		// Only the first correct placement wins the card, but every placement
		// is still judged and logged so the stats reflect what everyone did.
		if correct && !awarded {
			if err := insertIntoTimeline(gameId, placement.PlayerId, card.CardId, card.ReleaseYear, placement.Position); err != nil {
				return outcome, err
			}
			outcome.WinnerPlayerId = uuid.NullUUID{UUID: placement.PlayerId, Valid: true}
			outcome.WinnerName = placement.PlayerName
			outcome.WonByChallenge = placement.IsChallenge
			awarded = true
		}
	}

	if !awarded {
		if logErr := LogCardEvent(card.CardId, CardEventDiscarded); logErr != nil {
			log.Println(logErr)
		}
	}

	if err := ClearPlacements(gameId); err != nil {
		return outcome, err
	}
	if err := ClearGuesses(gameId); err != nil {
		return outcome, err
	}
	if err := SetRoundPhase(game.Id, PhaseReveal); err != nil {
		return outcome, err
	}

	return outcome, nil
}

// userIdForPlayer maps a player row back to its user, for the FK-free logs.
func userIdForPlayer(playerId uuid.UUID) (uuid.UUID, error) {
	var userId uuid.UUID
	rows, err := query("SELECT USER_ID FROM PLAYER WHERE ID = ?", playerId)
	if err != nil {
		return userId, err
	}
	defer rows.Close()

	for rows.Next() {
		if err := rows.Scan(&userId); err != nil {
			log.Println(err)
			return userId, errors.New("failed to scan row in query results")
		}
	}
	return userId, nil
}

// AdvanceToNextPlayer moves the turn to the next active player after the
// current one and starts a fresh listening phase with a new song.
func AdvanceToNextPlayer(gameId uuid.UUID) error {
	game, err := GetGameById(gameId)
	if err != nil {
		return err
	}

	players, err := GetPlayers(gameId)
	if err != nil {
		return err
	}
	if len(players) == 0 {
		return errors.New("no players in game")
	}

	currentIdx := -1
	for i, player := range players {
		if game.CurrentPlayerId.Valid && player.PlayerId == game.CurrentPlayerId.UUID {
			currentIdx = i
			break
		}
	}

	nextPlayerId := uuid.Nil
	for i := 1; i <= len(players); i++ {
		idx := (currentIdx + i) % len(players)
		if players[idx].IsActive {
			nextPlayerId = players[idx].PlayerId
			break
		}
	}
	if nextPlayerId == uuid.Nil {
		return errors.New("no active players found")
	}

	if err := SetCurrentPlayer(gameId, nextPlayerId); err != nil {
		return err
	}
	if err := SetRoundPhase(gameId, PhaseListening); err != nil {
		return err
	}

	return DrawCard(gameId)
}

// ChallengersOutstanding reports how many active players could still challenge:
// not the player on turn, holding at least one token, and not already placed.
// The window closes on its own once this reaches zero.
func ChallengersOutstanding(gameId uuid.UUID) (int, error) {
	game, err := GetGameById(gameId)
	if err != nil {
		return 0, err
	}

	players, err := GetPlayers(gameId)
	if err != nil {
		return 0, err
	}

	placements, err := GetPlacements(gameId)
	if err != nil {
		return 0, err
	}
	placed := make(map[uuid.UUID]bool, len(placements))
	for _, placement := range placements {
		placed[placement.PlayerId] = true
	}

	outstanding := 0
	for _, player := range players {
		if !player.IsActive {
			continue
		}
		if game.CurrentPlayerId.Valid && player.PlayerId == game.CurrentPlayerId.UUID {
			continue
		}
		if placed[player.PlayerId] {
			continue
		}
		if player.TokenCount < 1 {
			continue
		}
		outstanding++
	}

	return outstanding, nil
}

// SkipCurrentCard discards the song in play without judging anyone and draws a
// replacement for the same player. This is the escape hatch for a video that is
// dead, region-locked, or simply will not load — without it one bad link wedges
// the round for everyone.
func SkipCurrentCard(gameId uuid.UUID) error {
	card, err := GetCurrentCard(gameId)
	if err != nil {
		return err
	}
	if card.CardId == uuid.Nil {
		return errors.New("no song is in play")
	}

	if logErr := LogCardEvent(card.CardId, CardEventSkipped); logErr != nil {
		log.Println(logErr)
	}

	// The skip was nobody's fault, so the round restarts clean rather than
	// carrying over placements or guesses made against the abandoned song.
	if err := ClearPlacements(gameId); err != nil {
		return err
	}
	if err := ClearGuesses(gameId); err != nil {
		return err
	}
	if err := SetRoundPhase(gameId, PhaseListening); err != nil {
		return err
	}

	return DrawCard(gameId)
}
