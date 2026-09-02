package database

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"

	gsDatabase "github.com/gerp93/gameshell-framework/database"
	"github.com/google/uuid"
)

// Round phases. See TRACK_TIMELINE_GAME.sql for what each one permits.
const (
	PhaseListening = "listening"
	PhaseChallenge = "challenge"
	PhaseReveal    = "reveal"
)

// Game statuses.
const (
	StatusWaiting  = "waiting"
	StatusActive   = "active"
	StatusFinished = "finished"
)

// Bounds for the two lobby settings. MinCardsPerWinRatio exists because every
// round consumes exactly one card from the draw pile whether or not anyone wins
// it, so reaching CardsToWin takes several times that many rounds once other
// players' turns and missed placements are counted. Too small a pile for too
// high a target means the pile runs dry before anyone wins.
const (
	MinCardsToWin       = 5
	MaxCardsToWin       = 20
	MinCardsPerWinRatio = 4

	MinStartingTokens = 0
	MaxStartingTokens = 5
)

// ValidateCardsToWin reports why a target is unreachable, or nil if it is fine.
func ValidateCardsToWin(cardsToWin int, totalCards int) error {
	if cardsToWin < MinCardsToWin {
		return fmt.Errorf("cards to win (%d) is below the minimum of %d", cardsToWin, MinCardsToWin)
	}
	if cardsToWin > MaxCardsToWin {
		return fmt.Errorf("cards to win (%d) is above the maximum of %d", cardsToWin, MaxCardsToWin)
	}
	minRequired := cardsToWin * MinCardsPerWinRatio
	if totalCards < minRequired {
		return fmt.Errorf(
			"cards to win (%d) is too high for the selected decks and filters: %d matching song(s) found, at least %d are needed",
			cardsToWin, totalCards, minRequired,
		)
	}
	return nil
}

// ValidateStartingTokens bounds the per-player starting token count.
func ValidateStartingTokens(startingTokens int) error {
	if startingTokens < MinStartingTokens || startingTokens > MaxStartingTokens {
		return fmt.Errorf("starting tokens (%d) must be between %d and %d", startingTokens, MinStartingTokens, MaxStartingTokens)
	}
	return nil
}

// Game is one game's root state.
type Game struct {
	Id                    uuid.UUID
	LobbyId               uuid.UUID
	CreatedOnDate         time.Time
	CurrentPlayerId       uuid.NullUUID
	GameStatus            string
	RoundPhase            string
	ChallengeOpenedOnDate sql.NullTime
	CardsToWin            int
	StartingTokens        int
	WinnerId              uuid.NullUUID
}

// CurrentCard is everything about the song in play that is safe to send to any
// client at any time. It deliberately has no title, artist or year field: those
// are the answer, and a template that cannot reach them cannot leak them.
//
// The video id is here because it has to be — it is what plays the audio. A
// player with developer tools open can look it up, which this game treats the
// same way its tabletop ancestors do: as something nobody who wants to play
// will bother doing.
type CurrentCard struct {
	CardId             uuid.UUID
	YouTubeVideoId     string
	StartOffsetSeconds int
	CategoryName       sql.NullString
	DeckName           string
}

// CurrentCardAnswer adds the fields that may only be sent once the round has
// reached PhaseReveal. Fetching one is the deliberate act of revealing.
type CurrentCardAnswer struct {
	CurrentCard
	Title       string
	Artist      string
	ReleaseYear int
}

// TimelineCard is a card already won and placed. Unlike the card in play, its
// year is public: the board is what everyone reasons about.
type TimelineCard struct {
	Id           uuid.UUID
	CardId       uuid.UUID
	Title        string
	Artist       string
	ReleaseYear  int
	CategoryName sql.NullString
	Position     int
	PlacedOnDate time.Time
	// IsLastPlaced marks the most recently won card in the whole game so the
	// board can highlight it wherever it landed.
	IsLastPlaced bool
}

// Player is one seat at the table.
type Player struct {
	PlayerId     uuid.UUID
	UserId       uuid.UUID
	UserName     string
	IsActive     bool
	TimelineSize int
	TokenCount   int
	IsCurrent    bool
}

// PlayerTimeline is one player's row on the board.
type PlayerTimeline struct {
	PlayerId    uuid.UUID
	PlayerName  string
	IsCurrent   bool
	IsMe        bool
	TokenCount  int
	Timeline    []TimelineCard
	HasPlaced   bool
	PlacedAt    int
	IsChallenge bool
}

// DeckInfo is one deck's contribution to a draw pile, derived from the pile
// itself since a lobby's deck selection is not stored anywhere else.
type DeckInfo struct {
	DeckId         uuid.UUID
	Name           string
	RemainingCount int
	TotalCount     int
}

// YearRange is one inclusive era filter.
type YearRange struct {
	FromYear int
	ToYear   int
}

// CreateLobby delegates base lobby creation to the framework.
func CreateLobby(name string, message string, password string) (uuid.UUID, error) {
	return gsDatabase.CreateLobby(name, message, password)
}

// GetGame returns the game for a lobby, or the zero Game when there is none.
func GetGame(lobbyId uuid.UUID) (Game, error) {
	return getGameByColumn("LOBBY_ID", lobbyId)
}

// GetGameById returns a game by its own id.
func GetGameById(gameId uuid.UUID) (Game, error) {
	return getGameByColumn("ID", gameId)
}

func getGameByColumn(column string, value uuid.UUID) (Game, error) {
	var game Game

	sqlString := fmt.Sprintf(`
		SELECT
			ID,
			LOBBY_ID,
			CREATED_ON_DATE,
			CURRENT_PLAYER_ID,
			GAME_STATUS,
			ROUND_PHASE,
			CHALLENGE_OPENED_ON_DATE,
			CARDS_TO_WIN,
			STARTING_TOKENS,
			WINNER_ID
		FROM TRACK_TIMELINE_GAME
		WHERE %s = ?
	`, column)
	rows, err := query(sqlString, value)
	if err != nil {
		return game, err
	}
	defer rows.Close()

	for rows.Next() {
		if err := rows.Scan(
			&game.Id,
			&game.LobbyId,
			&game.CreatedOnDate,
			&game.CurrentPlayerId,
			&game.GameStatus,
			&game.RoundPhase,
			&game.ChallengeOpenedOnDate,
			&game.CardsToWin,
			&game.StartingTokens,
			&game.WinnerId,
		); err != nil {
			log.Println(err)
			return game, errors.New("failed to scan row in query results")
		}
	}

	return game, nil
}

// CreateGame creates the game row for a lobby.
func CreateGame(lobbyId uuid.UUID, cardsToWin int, startingTokens int) (uuid.UUID, error) {
	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return id, errors.New("failed to generate new id")
	}

	sqlString := `
		INSERT INTO TRACK_TIMELINE_GAME(ID, LOBBY_ID, CARDS_TO_WIN, STARTING_TOKENS)
		VALUES (?, ?, ?, ?)
	`
	return id, execute(sqlString, id, lobbyId, cardsToWin, startingTokens)
}

// InitializeDrawPile fills a game's pile with every playable card from the
// given decks: it must have a release year, and its genre must not be excluded.
func InitializeDrawPile(gameId uuid.UUID, deckIds []uuid.UUID, excludedCategoryIds []uuid.UUID) error {
	if len(deckIds) == 0 {
		return errors.New("no decks provided")
	}

	deckPlaceholders := strings.TrimSuffix(strings.Repeat("?,", len(deckIds)), ",")
	args := make([]any, 0, len(deckIds)+1+len(excludedCategoryIds))
	args = append(args, gameId)
	for _, deckId := range deckIds {
		args = append(args, deckId)
	}

	sqlString := `
		INSERT INTO TRACK_TIMELINE_DRAW_PILE (ID, TRACK_TIMELINE_GAME_ID, CARD_ID, RELEASE_YEAR)
		SELECT UUID(), ?, C.ID, C.RELEASE_YEAR
		FROM CARD C
		WHERE C.DECK_ID IN (` + deckPlaceholders + `)
			AND C.RELEASE_YEAR IS NOT NULL
	`
	if len(excludedCategoryIds) > 0 {
		categoryPlaceholders := strings.TrimSuffix(strings.Repeat("?,", len(excludedCategoryIds)), ",")
		sqlString += " AND (C.CATEGORY_ID IS NULL OR C.CATEGORY_ID NOT IN (" + categoryPlaceholders + "))"
		for _, categoryId := range excludedCategoryIds {
			args = append(args, categoryId)
		}
	}

	return execute(sqlString, args...)
}

// AddYearRange stores one inclusive era filter.
func AddYearRange(gameId uuid.UUID, fromYear int, toYear int) error {
	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return errors.New("failed to generate new id")
	}
	sqlString := `
		INSERT INTO TRACK_TIMELINE_YEAR_RANGE (ID, TRACK_TIMELINE_GAME_ID, FROM_YEAR, TO_YEAR)
		VALUES (?, ?, ?, ?)
	`
	return execute(sqlString, id, gameId, fromYear, toYear)
}

// GetYearRanges returns a game's era filters; empty means no filter.
func GetYearRanges(gameId uuid.UUID) ([]YearRange, error) {
	sqlString := `
		SELECT FROM_YEAR, TO_YEAR
		FROM TRACK_TIMELINE_YEAR_RANGE
		WHERE TRACK_TIMELINE_GAME_ID = ?
		ORDER BY FROM_YEAR
	`
	rows, err := query(sqlString, gameId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]YearRange, 0)
	for rows.Next() {
		var yearRange YearRange
		if err := rows.Scan(&yearRange.FromYear, &yearRange.ToYear); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, yearRange)
	}
	return result, nil
}

// ApplyYearRangeFilter drops pile cards outside every configured era. A game
// with no ranges keeps everything.
func ApplyYearRangeFilter(gameId uuid.UUID) error {
	ranges, err := GetYearRanges(gameId)
	if err != nil {
		return err
	}
	if len(ranges) == 0 {
		return nil
	}

	sqlString := `
		DELETE FROM TRACK_TIMELINE_DRAW_PILE
		WHERE TRACK_TIMELINE_GAME_ID = ?
			AND NOT EXISTS (
				SELECT 1
				FROM TRACK_TIMELINE_YEAR_RANGE R
				WHERE R.TRACK_TIMELINE_GAME_ID = ?
					AND RELEASE_YEAR BETWEEN R.FROM_YEAR AND R.TO_YEAR
			)
	`
	return execute(sqlString, gameId, gameId)
}

// CountCardsInDecksForRanges counts how many cards would land in a draw pile
// under the given filters, for the live estimate shown while setting a lobby up.
// Mirrors InitializeDrawPile plus ApplyYearRangeFilter exactly — no ranges means
// every year qualifies.
func CountCardsInDecksForRanges(deckIds []uuid.UUID, ranges []YearRange, excludedCategoryIds []uuid.UUID) (int, error) {
	if len(deckIds) == 0 {
		return 0, nil
	}

	deckPlaceholders := strings.TrimSuffix(strings.Repeat("?,", len(deckIds)), ",")
	args := make([]any, 0, len(deckIds)+2*len(ranges)+len(excludedCategoryIds))
	for _, id := range deckIds {
		args = append(args, id)
	}

	sqlString := `
		SELECT COUNT(*)
		FROM CARD
		WHERE DECK_ID IN (` + deckPlaceholders + `)
			AND RELEASE_YEAR IS NOT NULL
	`
	if len(ranges) > 0 {
		rangeClauses := make([]string, 0, len(ranges))
		for _, r := range ranges {
			rangeClauses = append(rangeClauses, "RELEASE_YEAR BETWEEN ? AND ?")
			args = append(args, r.FromYear, r.ToYear)
		}
		sqlString += " AND (" + strings.Join(rangeClauses, " OR ") + ")"
	}
	if len(excludedCategoryIds) > 0 {
		categoryPlaceholders := strings.TrimSuffix(strings.Repeat("?,", len(excludedCategoryIds)), ",")
		sqlString += " AND (CATEGORY_ID IS NULL OR CATEGORY_ID NOT IN (" + categoryPlaceholders + "))"
		for _, categoryId := range excludedCategoryIds {
			args = append(args, categoryId)
		}
	}

	rows, err := query(sqlString, args...)
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

// DrawCard makes a random undrawn pile card the song in play. Leaves no current
// card when the pile is exhausted; callers check for uuid.Nil.
func DrawCard(gameId uuid.UUID) error {
	if err := execute("DELETE FROM TRACK_TIMELINE_CURRENT_CARD WHERE TRACK_TIMELINE_GAME_ID = ?", gameId); err != nil {
		return err
	}

	sqlDraw := `
		INSERT INTO TRACK_TIMELINE_CURRENT_CARD (ID, TRACK_TIMELINE_GAME_ID, CARD_ID, RELEASE_YEAR)
		SELECT UUID(), ?, CARD_ID, RELEASE_YEAR
		FROM TRACK_TIMELINE_DRAW_PILE
		WHERE TRACK_TIMELINE_GAME_ID = ? AND DRAWN = 0
		ORDER BY RAND()
		LIMIT 1
	`
	if err := execute(sqlDraw, gameId, gameId); err != nil {
		return err
	}

	sqlMark := `
		UPDATE TRACK_TIMELINE_DRAW_PILE
		SET DRAWN = 1
		WHERE TRACK_TIMELINE_GAME_ID = ?
			AND CARD_ID = (SELECT CARD_ID FROM TRACK_TIMELINE_CURRENT_CARD WHERE TRACK_TIMELINE_GAME_ID = ?)
	`
	if err := execute(sqlMark, gameId, gameId); err != nil {
		return err
	}

	// Stats only; a failure here must not break the round.
	if card, err := GetCurrentCard(gameId); err == nil && card.CardId != uuid.Nil {
		if logErr := LogCardEvent(card.CardId, CardEventDrawn); logErr != nil {
			log.Println(logErr)
		}
	}

	return nil
}

// GetCurrentCard returns the song in play without its answer. This is the only
// form that may be sent to clients before the reveal.
func GetCurrentCard(gameId uuid.UUID) (CurrentCard, error) {
	var card CurrentCard

	sqlString := `
		SELECT CC.CARD_ID, C.YOUTUBE_VIDEO_ID, C.START_OFFSET_SECONDS, TC.NAME, COALESCE(D.NAME, '')
		FROM TRACK_TIMELINE_CURRENT_CARD CC
			INNER JOIN CARD C ON C.ID = CC.CARD_ID
			LEFT JOIN TRACK_TIMELINE_CATEGORY TC ON TC.ID = C.CATEGORY_ID
			LEFT JOIN DECK D ON D.ID = C.DECK_ID
		WHERE CC.TRACK_TIMELINE_GAME_ID = ?
	`
	rows, err := query(sqlString, gameId)
	if err != nil {
		return card, err
	}
	defer rows.Close()

	for rows.Next() {
		if err := rows.Scan(&card.CardId, &card.YouTubeVideoId, &card.StartOffsetSeconds, &card.CategoryName, &card.DeckName); err != nil {
			log.Println(err)
			return card, errors.New("failed to scan row in query results")
		}
	}

	return card, nil
}

// GetCurrentCardAnswer returns the song in play including title, artist and
// year. Only call this once the round has reached PhaseReveal, or server-side
// where the answer is being compared rather than sent.
func GetCurrentCardAnswer(gameId uuid.UUID) (CurrentCardAnswer, error) {
	var card CurrentCardAnswer

	sqlString := `
		SELECT CC.CARD_ID, C.YOUTUBE_VIDEO_ID, C.START_OFFSET_SECONDS, TC.NAME, COALESCE(D.NAME, ''),
			C.TITLE, C.ARTIST, CC.RELEASE_YEAR
		FROM TRACK_TIMELINE_CURRENT_CARD CC
			INNER JOIN CARD C ON C.ID = CC.CARD_ID
			LEFT JOIN TRACK_TIMELINE_CATEGORY TC ON TC.ID = C.CATEGORY_ID
			LEFT JOIN DECK D ON D.ID = C.DECK_ID
		WHERE CC.TRACK_TIMELINE_GAME_ID = ?
	`
	rows, err := query(sqlString, gameId)
	if err != nil {
		return card, err
	}
	defer rows.Close()

	for rows.Next() {
		if err := rows.Scan(
			&card.CardId, &card.YouTubeVideoId, &card.StartOffsetSeconds, &card.CategoryName, &card.DeckName,
			&card.Title, &card.Artist, &card.ReleaseYear,
		); err != nil {
			log.Println(err)
			return card, errors.New("failed to scan row in query results")
		}
	}

	return card, nil
}

// GetGameDecks reports each deck's contribution to the pile.
func GetGameDecks(gameId uuid.UUID) ([]DeckInfo, error) {
	sqlString := `
		SELECT
			D.ID,
			D.NAME,
			SUM(CASE WHEN DP.DRAWN = 0 THEN 1 ELSE 0 END) AS REMAINING_COUNT,
			COUNT(*) AS TOTAL_COUNT
		FROM TRACK_TIMELINE_DRAW_PILE DP
			INNER JOIN CARD C ON C.ID = DP.CARD_ID
			INNER JOIN DECK D ON D.ID = C.DECK_ID
		WHERE DP.TRACK_TIMELINE_GAME_ID = ?
		GROUP BY D.ID, D.NAME
		ORDER BY D.NAME ASC
	`
	rows, err := query(sqlString, gameId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]DeckInfo, 0)
	for rows.Next() {
		var deck DeckInfo
		if err := rows.Scan(&deck.DeckId, &deck.Name, &deck.RemainingCount, &deck.TotalCount); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, deck)
	}

	return result, nil
}

// GetDrawPileCount reports how many songs are left undrawn.
func GetDrawPileCount(gameId uuid.UUID) (int, error) {
	sqlString := `
		SELECT COUNT(*)
		FROM TRACK_TIMELINE_DRAW_PILE
		WHERE TRACK_TIMELINE_GAME_ID = ? AND DRAWN = 0
	`
	rows, err := query(sqlString, gameId)
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

// GetPlayers returns every player in turn order, with timeline size and token
// balance. Rows stay put as turns pass — the current player is flagged rather
// than hoisted, so the board does not reshuffle underneath everyone mid-game.
func GetPlayers(gameId uuid.UUID) ([]Player, error) {
	sqlString := `
		SELECT
			P.ID,
			P.USER_ID,
			U.NAME,
			P.IS_ACTIVE,
			COALESCE((
				SELECT COUNT(*) FROM TRACK_TIMELINE_PLAYER_TIMELINE PT
				WHERE PT.TRACK_TIMELINE_GAME_ID = G.ID AND PT.PLAYER_ID = P.ID
			), 0) AS TIMELINE_SIZE,
			COALESCE((
				SELECT PTK.TOKEN_COUNT FROM TRACK_TIMELINE_PLAYER_TOKEN PTK
				WHERE PTK.TRACK_TIMELINE_GAME_ID = G.ID AND PTK.PLAYER_ID = P.ID
			), G.STARTING_TOKENS) AS TOKEN_COUNT,
			CASE WHEN G.CURRENT_PLAYER_ID = P.ID THEN 1 ELSE 0 END AS IS_CURRENT
		FROM TRACK_TIMELINE_GAME G
			INNER JOIN LOBBY L ON L.ID = G.LOBBY_ID
			INNER JOIN PLAYER P ON P.LOBBY_ID = L.ID
			INNER JOIN USER U ON U.ID = P.USER_ID
			LEFT JOIN TRACK_TIMELINE_PLAYER_ORDER PO
				ON PO.TRACK_TIMELINE_GAME_ID = G.ID AND PO.PLAYER_ID = P.ID
		WHERE G.ID = ?
		ORDER BY COALESCE(PO.TURN_ORDER, 1000000 + P.JOIN_ORDER) ASC, P.JOIN_ORDER ASC
	`
	rows, err := query(sqlString, gameId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]Player, 0)
	for rows.Next() {
		var player Player
		if err := rows.Scan(
			&player.PlayerId,
			&player.UserId,
			&player.UserName,
			&player.IsActive,
			&player.TimelineSize,
			&player.TokenCount,
			&player.IsCurrent,
		); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, player)
	}

	return result, nil
}

// GetPlayerTimeline returns one player's placed cards in order.
func GetPlayerTimeline(gameId uuid.UUID, playerId uuid.UUID) ([]TimelineCard, error) {
	sqlString := `
		SELECT PT.ID, PT.CARD_ID, C.TITLE, C.ARTIST, PT.RELEASE_YEAR, TC.NAME, PT.POSITION, PT.PLACED_ON_DATE
		FROM TRACK_TIMELINE_PLAYER_TIMELINE PT
			INNER JOIN CARD C ON C.ID = PT.CARD_ID
			LEFT JOIN TRACK_TIMELINE_CATEGORY TC ON TC.ID = C.CATEGORY_ID
		WHERE PT.TRACK_TIMELINE_GAME_ID = ? AND PT.PLAYER_ID = ?
		ORDER BY PT.POSITION ASC
	`
	rows, err := query(sqlString, gameId, playerId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]TimelineCard, 0)
	for rows.Next() {
		var card TimelineCard
		if err := rows.Scan(
			&card.Id,
			&card.CardId,
			&card.Title,
			&card.Artist,
			&card.ReleaseYear,
			&card.CategoryName,
			&card.Position,
			&card.PlacedOnDate,
		); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, card)
	}

	return result, nil
}

// GetLastPlacedCardId returns the card most recently won by anyone, or uuid.Nil.
func GetLastPlacedCardId(gameId uuid.UUID) (uuid.UUID, error) {
	var cardId uuid.UUID

	sqlString := `
		SELECT CARD_ID
		FROM TRACK_TIMELINE_PLAYER_TIMELINE
		WHERE TRACK_TIMELINE_GAME_ID = ?
		ORDER BY PLACED_ON_DATE DESC, ID DESC
		LIMIT 1
	`
	rows, err := query(sqlString, gameId)
	if err != nil {
		return cardId, err
	}
	defer rows.Close()

	for rows.Next() {
		if err := rows.Scan(&cardId); err != nil {
			log.Println(err)
			return cardId, errors.New("failed to scan row in query results")
		}
	}

	return cardId, nil
}

// GetAllPlayerTimelines assembles the whole board for one viewer.
//
// The placement markers it returns are phase-aware: during PhaseChallenge a
// player sees only that others have committed, never where, because seeing a
// rival's guess before choosing your own would make the challenge a copy rather
// than a judgement. At PhaseReveal every position becomes visible.
func GetAllPlayerTimelines(gameId uuid.UUID, currentPlayerId uuid.UUID, viewingPlayerId uuid.UUID, revealPositions bool) ([]PlayerTimeline, error) {
	players, err := GetPlayers(gameId)
	if err != nil {
		return nil, err
	}

	placements, err := GetPlacements(gameId)
	if err != nil {
		return nil, err
	}
	placementByPlayer := make(map[uuid.UUID]Placement, len(placements))
	for _, placement := range placements {
		placementByPlayer[placement.PlayerId] = placement
	}

	lastPlacedCardId, err := GetLastPlacedCardId(gameId)
	if err != nil {
		lastPlacedCardId = uuid.Nil
	}

	result := make([]PlayerTimeline, 0, len(players))
	for _, player := range players {
		if !player.IsActive {
			continue
		}
		timeline, err := GetPlayerTimeline(gameId, player.PlayerId)
		if err != nil {
			timeline = []TimelineCard{}
		}
		for i := range timeline {
			timeline[i].IsLastPlaced = lastPlacedCardId != uuid.Nil && timeline[i].CardId == lastPlacedCardId
		}

		row := PlayerTimeline{
			PlayerId:   player.PlayerId,
			PlayerName: player.UserName,
			IsCurrent:  player.PlayerId == currentPlayerId,
			IsMe:       player.PlayerId == viewingPlayerId,
			TokenCount: player.TokenCount,
			Timeline:   timeline,
		}
		if placement, ok := placementByPlayer[player.PlayerId]; ok {
			row.HasPlaced = true
			row.IsChallenge = placement.IsChallenge
			// Your own marker is always yours to see; everyone else's waits
			// for the reveal.
			if revealPositions || player.PlayerId == viewingPlayerId {
				row.PlacedAt = placement.Position
			} else {
				row.PlacedAt = -1
			}
		}
		result = append(result, row)
	}

	return result, nil
}

// SetCurrentPlayer sets whose turn it is.
func SetCurrentPlayer(gameId uuid.UUID, playerId uuid.UUID) error {
	return execute("UPDATE TRACK_TIMELINE_GAME SET CURRENT_PLAYER_ID = ? WHERE ID = ?", playerId, gameId)
}

// SetRoundPhase moves the round to a new phase, stamping the challenge window's
// start when entering it and clearing the stamp otherwise.
func SetRoundPhase(gameId uuid.UUID, phase string) error {
	if phase == PhaseChallenge {
		return execute(
			"UPDATE TRACK_TIMELINE_GAME SET ROUND_PHASE = ?, CHALLENGE_OPENED_ON_DATE = CURRENT_TIMESTAMP() WHERE ID = ?",
			phase, gameId,
		)
	}
	return execute(
		"UPDATE TRACK_TIMELINE_GAME SET ROUND_PHASE = ?, CHALLENGE_OPENED_ON_DATE = NULL WHERE ID = ?",
		phase, gameId,
	)
}

// ShufflePlayerOrder randomizes turn order, replacing any previous one, and
// guarantees the new order differs from the immediately previous one rather
// than merely being likely to: a fresh rand.Shuffle reproduces the same
// sequence 1 time in N!, which is exactly the case a "different from last time"
// rule exists to rule out.
func ShufflePlayerOrder(gameId uuid.UUID) error {
	// GetPlayers orders by the existing TURN_ORDER (falling back to JOIN_ORDER
	// when none has ever been set), so this doubles as the previous order.
	players, err := GetPlayers(gameId)
	if err != nil {
		return err
	}

	previous := make([]uuid.UUID, 0, len(players))
	next := make([]uuid.UUID, 0, len(players))
	for _, player := range players {
		if player.IsActive {
			previous = append(previous, player.PlayerId)
			next = append(next, player.PlayerId)
		}
	}
	if len(next) == 0 {
		return errors.New("no active players to order")
	}

	// Bounded, not infinite: with one active player there is only one possible
	// order, so "different from last time" can never be satisfied and the loop
	// simply keeps whatever the last shuffle produced.
	for attempt := 0; attempt < 20; attempt++ {
		rand.Shuffle(len(next), func(i, j int) {
			next[i], next[j] = next[j], next[i]
		})
		if !sameUUIDOrder(next, previous) {
			break
		}
	}

	if err := execute("DELETE FROM TRACK_TIMELINE_PLAYER_ORDER WHERE TRACK_TIMELINE_GAME_ID = ?", gameId); err != nil {
		return err
	}

	sqlInsert := `
		INSERT INTO TRACK_TIMELINE_PLAYER_ORDER (ID, TRACK_TIMELINE_GAME_ID, PLAYER_ID, TURN_ORDER)
		VALUES (?, ?, ?, ?)
	`
	for turnOrder, playerId := range next {
		id, err := uuid.NewUUID()
		if err != nil {
			log.Println(err)
			return errors.New("failed to generate new id")
		}
		if err := execute(sqlInsert, id, gameId, playerId, turnOrder); err != nil {
			return err
		}
	}

	return nil
}

func sameUUIDOrder(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// StartGame shuffles turn order, deals one song to each player as the seed of
// their timeline, and draws the first song to play.
func StartGame(gameId uuid.UUID) error {
	// Shuffle before anything reads the order: dealing, the first player, and
	// the board's row order all come from GetPlayers.
	if err := ShufflePlayerOrder(gameId); err != nil {
		return err
	}

	players, err := GetPlayers(gameId)
	if err != nil {
		return err
	}
	if len(players) == 0 {
		return errors.New("no players in game")
	}

	game, err := GetGameById(gameId)
	if err != nil {
		return err
	}

	for _, player := range players {
		if !player.IsActive {
			continue
		}

		cardId, releaseYear, err := takeCardFromPile(gameId)
		if err != nil {
			return err
		}

		id, err := uuid.NewUUID()
		if err != nil {
			log.Println(err)
			return errors.New("failed to generate new id")
		}
		sqlAdd := `
			INSERT INTO TRACK_TIMELINE_PLAYER_TIMELINE (ID, TRACK_TIMELINE_GAME_ID, PLAYER_ID, CARD_ID, RELEASE_YEAR, POSITION)
			VALUES (?, ?, ?, ?, ?, 0)
		`
		if err := execute(sqlAdd, id, gameId, player.PlayerId, cardId, releaseYear); err != nil {
			return err
		}

		if err := SetPlayerTokens(gameId, player.PlayerId, game.StartingTokens); err != nil {
			return err
		}
	}

	var firstPlayer uuid.UUID
	for _, player := range players {
		if player.IsActive {
			firstPlayer = player.PlayerId
			break
		}
	}
	if firstPlayer == uuid.Nil {
		return errors.New("no active players")
	}

	sqlStart := `
		UPDATE TRACK_TIMELINE_GAME
		SET GAME_STATUS = ?, ROUND_PHASE = ?, CHALLENGE_OPENED_ON_DATE = NULL, CURRENT_PLAYER_ID = ?
		WHERE ID = ?
	`
	if err := execute(sqlStart, StatusActive, PhaseListening, firstPlayer, gameId); err != nil {
		return err
	}

	return DrawCard(gameId)
}

// takeCardFromPile pulls one random undrawn card out of the pile and marks it
// drawn, returning its id and year.
func takeCardFromPile(gameId uuid.UUID) (uuid.UUID, int, error) {
	var cardId uuid.UUID
	var releaseYear int

	sqlGet := `
		SELECT CARD_ID, RELEASE_YEAR
		FROM TRACK_TIMELINE_DRAW_PILE
		WHERE TRACK_TIMELINE_GAME_ID = ? AND DRAWN = 0
		ORDER BY RAND()
		LIMIT 1
	`
	rows, err := query(sqlGet, gameId)
	if err != nil {
		return cardId, releaseYear, err
	}
	defer rows.Close()

	if !rows.Next() {
		return cardId, releaseYear, errors.New("not enough songs to deal a starting card to every player")
	}
	if err := rows.Scan(&cardId, &releaseYear); err != nil {
		log.Println(err)
		return cardId, releaseYear, errors.New("failed to scan row in query results")
	}
	rows.Close()

	sqlMark := "UPDATE TRACK_TIMELINE_DRAW_PILE SET DRAWN = 1 WHERE TRACK_TIMELINE_GAME_ID = ? AND CARD_ID = ?"
	if err := execute(sqlMark, gameId, cardId); err != nil {
		return cardId, releaseYear, err
	}

	return cardId, releaseYear, nil
}

// ResetGame returns a finished game to the waiting state so the same lobby can
// play again.
func ResetGame(gameId uuid.UUID) error {
	for _, sqlString := range []string{
		"DELETE FROM TRACK_TIMELINE_PLAYER_TIMELINE WHERE TRACK_TIMELINE_GAME_ID = ?",
		"DELETE FROM TRACK_TIMELINE_CURRENT_CARD WHERE TRACK_TIMELINE_GAME_ID = ?",
		"DELETE FROM TRACK_TIMELINE_PLACEMENT WHERE TRACK_TIMELINE_GAME_ID = ?",
		"DELETE FROM TRACK_TIMELINE_TITLE_GUESS WHERE TRACK_TIMELINE_GAME_ID = ?",
		"DELETE FROM TRACK_TIMELINE_PLAYER_TOKEN WHERE TRACK_TIMELINE_GAME_ID = ?",
	} {
		if err := execute(sqlString, gameId); err != nil {
			return err
		}
	}

	// TRACK_TIMELINE_PLAYER_ORDER is deliberately NOT cleared: the next
	// StartGame reshuffles it anyway, and ShufflePlayerOrder needs these rows
	// intact as the baseline that guarantees the new order differs from this
	// one. Clearing them silently downgrades that guarantee to a coin flip.

	sqlResetPile := "UPDATE TRACK_TIMELINE_DRAW_PILE SET DRAWN = 0 WHERE TRACK_TIMELINE_GAME_ID = ?"
	if err := execute(sqlResetPile, gameId); err != nil {
		return err
	}

	sqlResetGame := `
		UPDATE TRACK_TIMELINE_GAME
		SET GAME_STATUS = ?, ROUND_PHASE = ?, CHALLENGE_OPENED_ON_DATE = NULL,
			CURRENT_PLAYER_ID = NULL, WINNER_ID = NULL
		WHERE ID = ?
	`
	return execute(sqlResetGame, StatusWaiting, PhaseListening, gameId)
}

// CheckWinner marks and returns the first player to reach the target, or
// uuid.Nil when nobody has.
func CheckWinner(gameId uuid.UUID) (uuid.UUID, error) {
	game, err := GetGameById(gameId)
	if err != nil {
		return uuid.Nil, err
	}

	players, err := GetPlayers(gameId)
	if err != nil {
		return uuid.Nil, err
	}

	for _, player := range players {
		if player.TimelineSize >= game.CardsToWin {
			sqlString := "UPDATE TRACK_TIMELINE_GAME SET GAME_STATUS = ?, WINNER_ID = ? WHERE ID = ?"
			if err := execute(sqlString, StatusFinished, player.UserId, gameId); err != nil {
				return uuid.Nil, err
			}
			activeCount := 0
			for _, p := range players {
				if p.IsActive {
					activeCount++
				}
			}
			if logErr := LogWin(player.UserId, game.CardsToWin, activeCount); logErr != nil {
				log.Println(logErr)
			}
			return player.UserId, nil
		}
	}

	return uuid.Nil, nil
}

// LobbyDetails is one row in the lobby list.
type LobbyDetails struct {
	Id          uuid.UUID
	Name        string
	PlayerCount int
	GameStatus  string
	HasPassword bool
}

// SearchLobbies returns one page of lobbies matching name.
func SearchLobbies(name string, page int) ([]LobbyDetails, error) {
	name = "%" + name + "%"
	if page < 1 {
		page = 1
	}

	sqlString := `
		SELECT
			L.ID,
			L.NAME,
			L.PASSWORD_HASH IS NOT NULL AS HAS_PASSWORD,
			COALESCE(G.GAME_STATUS, 'waiting') AS GAME_STATUS,
			COUNT(P.ID) AS PLAYER_COUNT
		FROM LOBBY AS L
			LEFT JOIN TRACK_TIMELINE_GAME AS G ON G.LOBBY_ID = L.ID
			LEFT JOIN PLAYER AS P ON P.LOBBY_ID = L.ID AND P.IS_ACTIVE = 1
		WHERE L.NAME LIKE ?
		GROUP BY L.ID
		ORDER BY L.CREATED_ON_DATE DESC
		LIMIT 10 OFFSET ?
	`
	rows, err := query(sqlString, name, (page-1)*10)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]LobbyDetails, 0)
	for rows.Next() {
		var details LobbyDetails
		if err := rows.Scan(
			&details.Id,
			&details.Name,
			&details.HasPassword,
			&details.GameStatus,
			&details.PlayerCount,
		); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, details)
	}
	return result, nil
}

// CountLobbies counts lobbies matching name.
func CountLobbies(name string) (int, error) {
	name = "%" + name + "%"

	rows, err := query("SELECT COUNT(*) FROM LOBBY WHERE NAME LIKE ?", name)
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
