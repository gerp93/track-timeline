package database

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Placement is the turn player's own committed guess at where the song in
// play belongs. At most one exists per round -- the stealer's attempt (see
// ClaimSteal) is judged and resolved immediately rather than recorded here.
type Placement struct {
	Id             uuid.UUID
	CreatedOnDate  time.Time
	PlayerId       uuid.UUID
	PlayerName     string
	Position       int
	ExactYearGuess sql.NullInt64
	YearWager      int
	// YearRange is snapshotted at CommitPlacement so a steal compares against
	// the locked-in window even if the original player's timeline changes
	// during the steal window (e.g. a buy).
	YearRange PlacementYearRange
}

// GetPlacement returns the turn player's placement this round. The zero
// Placement (Id == uuid.Nil) means nobody has placed yet.
func GetPlacement(gameId uuid.UUID) (Placement, error) {
	var placement Placement

	sqlString := `
		SELECT PL.ID, PL.CREATED_ON_DATE, PL.PLAYER_ID, U.NAME, PL.POSITION,
			PL.EXACT_YEAR_GUESS, PL.YEAR_WAGER,
			PL.RANGE_HAS_LOWER, PL.RANGE_LOWER, PL.RANGE_HAS_UPPER, PL.RANGE_UPPER
		FROM TRACK_TIMELINE_PLACEMENT PL
			INNER JOIN PLAYER P ON P.ID = PL.PLAYER_ID
			INNER JOIN USER U ON U.ID = P.USER_ID
		WHERE PL.TRACK_TIMELINE_GAME_ID = ?
	`
	rows, err := query(sqlString, gameId)
	if err != nil {
		return placement, err
	}
	defer rows.Close()

	for rows.Next() {
		if err := rows.Scan(
			&placement.Id,
			&placement.CreatedOnDate,
			&placement.PlayerId,
			&placement.PlayerName,
			&placement.Position,
			&placement.ExactYearGuess,
			&placement.YearWager,
			&placement.YearRange.HasLower,
			&placement.YearRange.Lower,
			&placement.YearRange.HasUpper,
			&placement.YearRange.Upper,
		); err != nil {
			log.Println(err)
			return placement, errors.New("failed to scan row in query results")
		}
	}

	return placement, nil
}

// HasPlaced reports whether playerId is the turn player and has already
// committed a placement this round.
func HasPlaced(gameId uuid.UUID, playerId uuid.UUID) (bool, error) {
	placement, err := GetPlacement(gameId)
	if err != nil {
		return false, err
	}
	return placement.Id != uuid.Nil && placement.PlayerId == playerId, nil
}

// CommitPlacement records the turn player's placement. The GAME_PLAYER_UNIQUE
// constraint means a second call this round is a conflict rather than an
// overwrite — you do not get to move your guess once it is in.
// exactYearGuess / yearWager are set when the player used the exact-year
// wager; pass yearWager 0 for an ordinary slot placement.
// yearRange is the slot's year window at lock-in (see PlacementYearRangeOf).
func CommitPlacement(gameId uuid.UUID, playerId uuid.UUID, position int, exactYearGuess sql.NullInt64, yearWager int, yearRange PlacementYearRange) error {
	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return errors.New("failed to generate new id")
	}

	sqlString := `
		INSERT INTO TRACK_TIMELINE_PLACEMENT (
			ID, TRACK_TIMELINE_GAME_ID, PLAYER_ID, POSITION,
			EXACT_YEAR_GUESS, YEAR_WAGER,
			RANGE_HAS_LOWER, RANGE_LOWER, RANGE_HAS_UPPER, RANGE_UPPER
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	return execute(
		sqlString,
		id, gameId, playerId, position,
		exactYearGuess, yearWager,
		yearRange.HasLower, yearRange.Lower, yearRange.HasUpper, yearRange.Upper,
	)
}

// ClearPlacements empties the round's placement.
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

// Guess is one player's guess about one song of the round in progress. A round
// can span several songs -- every song skipped before the one that is finally
// placed -- and a guess stays on file against the song it was typed for.
//
// A guess is a draft (Locked false) until the player presses Guess, and the
// browser saves drafts as they are typed so a song skipped or placed first still
// credits whatever was in the boxes. Nothing is judged until the round resolves
// (JudgeRoundGuesses); the verdict fields mean nothing before Judged is true.
type Guess struct {
	Id         uuid.UUID
	UserId     uuid.UUID
	PlayerId   uuid.UUID
	PlayerName string

	// CardId is the song the guess was typed for. SongTitle and SongArtist are
	// its answer: server-side only, never for a template or a payload sent
	// before the reveal.
	CardId     uuid.UUID
	SongTitle  string
	SongArtist string
	// IsCurrentSong is false for a song skipped earlier in the round.
	IsCurrentSong bool

	TitleGuess  string
	ArtistGuess string
	// GuessText is the combined "title by artist" form, for chat quoting.
	GuessText string
	Locked    bool
	Judged    bool

	TitleCorrect       bool
	ArtistCorrect      bool
	TitleMatchPercent  int
	ArtistMatchPercent int
	// JudgedByAI is guess.Verdict.ByAI, persisted so it survives past the
	// moment of judging -- the reveal chat line only has this stored row.
	JudgedByAI bool
}

// CombineGuess is the one "title by artist" form of a split guess, used for
// storage and chat quoting.
func CombineGuess(title, artist string) string {
	switch {
	case title != "" && artist != "":
		return title + " by " + artist
	case title != "":
		return title
	default:
		return artist
	}
}

// guessSelect is the SELECT list shared by every query that reads a Guess,
// scanned by scanGuess. The answer columns come from CARD, never from what the
// player typed.
const guessSelect = `
	SELECT G.ID, U.ID, G.PLAYER_ID, U.NAME, G.CARD_ID, C.TITLE, C.ARTIST,
		COALESCE(G.CARD_ID = CC.CARD_ID, 0),
		G.TITLE_GUESS, G.ARTIST_GUESS, G.GUESS_TEXT, G.LOCKED, G.JUDGED,
		G.TITLE_CORRECT, G.ARTIST_CORRECT, G.TITLE_MATCH_PERCENT, G.ARTIST_MATCH_PERCENT, G.JUDGED_BY_AI
	FROM TRACK_TIMELINE_TITLE_GUESS G
		INNER JOIN PLAYER P ON P.ID = G.PLAYER_ID
		INNER JOIN USER U ON U.ID = P.USER_ID
		INNER JOIN CARD C ON C.ID = G.CARD_ID
		LEFT JOIN TRACK_TIMELINE_CURRENT_CARD CC ON CC.TRACK_TIMELINE_GAME_ID = G.TRACK_TIMELINE_GAME_ID
		LEFT JOIN TRACK_TIMELINE_SKIPPED_CARD SK
			ON SK.TRACK_TIMELINE_GAME_ID = G.TRACK_TIMELINE_GAME_ID AND SK.CARD_ID = G.CARD_ID
`

// guessOrder lists songs in the order they were played -- skipped ones as they
// were skipped, the song in play last -- then guesses in the order they began.
const guessOrder = `
	ORDER BY COALESCE(G.CARD_ID = CC.CARD_ID, 0) ASC, SK.CREATED_ON_DATE ASC, G.CREATED_ON_DATE ASC
`

func scanGuesses(rows *sql.Rows) ([]Guess, error) {
	result := make([]Guess, 0)
	for rows.Next() {
		var g Guess
		if err := rows.Scan(
			&g.Id, &g.UserId, &g.PlayerId, &g.PlayerName, &g.CardId, &g.SongTitle, &g.SongArtist,
			&g.IsCurrentSong,
			&g.TitleGuess, &g.ArtistGuess, &g.GuessText, &g.Locked, &g.Judged,
			&g.TitleCorrect, &g.ArtistCorrect, &g.TitleMatchPercent, &g.ArtistMatchPercent, &g.JudgedByAI,
		); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, g)
	}
	return result, nil
}

// GetGuesses returns every guess of the round in progress that has something in
// it, across every song of the round, in the order the songs were played.
func GetGuesses(gameId uuid.UUID) ([]Guess, error) {
	rows, err := query(guessSelect+" WHERE G.TRACK_TIMELINE_GAME_ID = ? AND G.GUESS_TEXT <> ''"+guessOrder, gameId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGuesses(rows)
}

// GetPlayerGuess returns this player's guess about the song in play, with ok
// false when they have none (no draft, no lock).
func GetPlayerGuess(gameId uuid.UUID, playerId uuid.UUID) (Guess, bool, error) {
	rows, err := query(
		guessSelect+" WHERE G.TRACK_TIMELINE_GAME_ID = ? AND G.PLAYER_ID = ? AND G.CARD_ID = CC.CARD_ID",
		gameId, playerId,
	)
	if err != nil {
		return Guess{}, false, err
	}
	defer rows.Close()
	guesses, err := scanGuesses(rows)
	if err != nil || len(guesses) == 0 {
		return Guess{}, false, err
	}
	return guesses[0], true, nil
}

// HasGuessed reports whether a player has locked in a guess about the song in
// play. One locked guess per player per song is what stops someone spraying
// attempts until one lands; a guess about a song skipped earlier in the round is
// a different song's, and does not count.
func HasGuessed(gameId uuid.UUID, playerId uuid.UUID) (bool, error) {
	sqlString := `
		SELECT COUNT(*)
		FROM TRACK_TIMELINE_TITLE_GUESS G
			INNER JOIN TRACK_TIMELINE_CURRENT_CARD CC
				ON CC.TRACK_TIMELINE_GAME_ID = G.TRACK_TIMELINE_GAME_ID AND CC.CARD_ID = G.CARD_ID
		WHERE G.TRACK_TIMELINE_GAME_ID = ? AND G.PLAYER_ID = ? AND G.LOCKED = 1
	`
	count, err := countRows(sqlString, gameId, playerId)
	return count > 0, err
}

// CountLockedGuesses is how many players have locked in a guess about the song
// in play, for the "N of M guessed so far" banner. It says who has guessed, never
// what.
func CountLockedGuesses(gameId uuid.UUID) (int, error) {
	sqlString := `
		SELECT COUNT(*)
		FROM TRACK_TIMELINE_TITLE_GUESS G
			INNER JOIN TRACK_TIMELINE_CURRENT_CARD CC
				ON CC.TRACK_TIMELINE_GAME_ID = G.TRACK_TIMELINE_GAME_ID AND CC.CARD_ID = G.CARD_ID
		WHERE G.TRACK_TIMELINE_GAME_ID = ? AND G.LOCKED = 1
	`
	return countRows(sqlString, gameId)
}

func countRows(sqlString string, params ...any) (int, error) {
	rows, err := query(sqlString, params...)
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

// IsRoundCard reports whether a song belongs to the round in progress: the one in
// play, or one skipped earlier in the round. A guess tagged with anything else is
// for a song that is over, and is refused rather than being judged against the
// wrong one.
func IsRoundCard(gameId uuid.UUID, cardId uuid.UUID) (bool, error) {
	sqlString := `
		SELECT COUNT(*) FROM (
			SELECT CARD_ID FROM TRACK_TIMELINE_CURRENT_CARD WHERE TRACK_TIMELINE_GAME_ID = ? AND CARD_ID = ?
			UNION ALL
			SELECT CARD_ID FROM TRACK_TIMELINE_SKIPPED_CARD WHERE TRACK_TIMELINE_GAME_ID = ? AND CARD_ID = ?
		) ROUND_CARDS
	`
	count, err := countRows(sqlString, gameId, cardId, gameId, cardId)
	return count > 0, err
}

// ErrGuessLocked means the player already locked in a guess about this song.
var ErrGuessLocked = errors.New("already guessed")

// SaveGuess stores what a player has typed about a song. With lock false it is a
// draft: it replaces any earlier draft, and an empty one removes the row. With
// lock true it is the player pressing Guess: it replaces the draft and locks it,
// and ErrGuessLocked comes back if they had already locked one. A locked or
// already-judged guess is never changed by a later draft, so a browser saving a
// draft a moment late cannot rewrite what was submitted.
//
// Nothing is judged here; see JudgeRoundGuesses.
func SaveGuess(gameId, playerId, cardId uuid.UUID, titleGuess, artistGuess string, lock bool) error {
	combined := CombineGuess(titleGuess, artistGuess)

	if lock {
		if existing, err := countRows(
			"SELECT COUNT(*) FROM TRACK_TIMELINE_TITLE_GUESS WHERE TRACK_TIMELINE_GAME_ID = ? AND PLAYER_ID = ? AND CARD_ID = ? AND (LOCKED = 1 OR JUDGED = 1)",
			gameId, playerId, cardId,
		); err != nil {
			return err
		} else if existing > 0 {
			return ErrGuessLocked
		}
	} else if combined == "" {
		return execute(
			"DELETE FROM TRACK_TIMELINE_TITLE_GUESS WHERE TRACK_TIMELINE_GAME_ID = ? AND PLAYER_ID = ? AND CARD_ID = ? AND LOCKED = 0 AND JUDGED = 0",
			gameId, playerId, cardId,
		)
	}

	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return errors.New("failed to generate new id")
	}

	sqlString := `
		INSERT INTO TRACK_TIMELINE_TITLE_GUESS
			(ID, TRACK_TIMELINE_GAME_ID, PLAYER_ID, CARD_ID, TITLE_GUESS, ARTIST_GUESS, GUESS_TEXT, LOCKED)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			TITLE_GUESS = IF(LOCKED = 1 OR JUDGED = 1, TITLE_GUESS, VALUES(TITLE_GUESS)),
			ARTIST_GUESS = IF(LOCKED = 1 OR JUDGED = 1, ARTIST_GUESS, VALUES(ARTIST_GUESS)),
			GUESS_TEXT = IF(LOCKED = 1 OR JUDGED = 1, GUESS_TEXT, VALUES(GUESS_TEXT)),
			LOCKED = IF(JUDGED = 1, LOCKED, LOCKED OR VALUES(LOCKED))
	`
	return execute(sqlString, id, gameId, playerId, cardId, titleGuess, artistGuess, combined, lock)
}

// GuessTokensEarned is how many tokens a judged guess pays: GuessTokensPerPart
// for the right title and again for the right artist, so 0, 1 or 2. The two
// parts are scored independently, so getting only one right still pays.
func GuessTokensEarned(g Guess) int {
	earned := 0
	if g.TitleCorrect {
		earned += GuessTokensPerPart
	}
	if g.ArtistCorrect {
		earned += GuessTokensPerPart
	}
	return earned
}

// GuessTokenWinners lists this round's judged guesses that earned their player
// at least a token, in the order the songs were played. It only reads: the
// tokens were already paid when the guesses were judged (see
// JudgeRoundGuesses), so this exists for the reveal, which announces who earned
// what (GuessTokensEarned on each one).
func GuessTokenWinners(gameId uuid.UUID) ([]Guess, error) {
	guesses, err := GetGuesses(gameId)
	if err != nil {
		return nil, err
	}

	var winners []Guess
	for _, g := range guesses {
		if g.Judged && GuessTokensEarned(g) > 0 {
			winners = append(winners, g)
		}
	}
	return winners, nil
}

// AwardGuessToken pays a judged guess its tokens. It returns how many were paid
// (0, 1 or 2), and does nothing for a guess that earned none.
//
// This is deliberately separate from ResolveRound's placement/card judging:
// the guess-token economy and the card economy are independent.
func AwardGuessToken(gameId uuid.UUID, playerId uuid.UUID, g Guess) (int, error) {
	earned := GuessTokensEarned(g)
	if earned == 0 {
		return 0, nil
	}
	if _, err := AddPlayerTokens(gameId, playerId, earned); err != nil {
		return 0, err
	}
	return earned, nil
}

// AddSkippedCard records a song as skipped during the round in progress, so the
// guesses made on it stay valid and are judged with the rest of the round.
func AddSkippedCard(gameId uuid.UUID, cardId uuid.UUID) error {
	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return errors.New("failed to generate new id")
	}
	return execute(
		"INSERT INTO TRACK_TIMELINE_SKIPPED_CARD (ID, TRACK_TIMELINE_GAME_ID, CARD_ID) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE CARD_ID = CARD_ID",
		id, gameId, cardId,
	)
}

// DiscardGuessesForCard drops every guess made about one song, for a song that
// never really played (a dead video), where judging them would be unfair.
func DiscardGuessesForCard(gameId uuid.UUID, cardId uuid.UUID) error {
	return execute("DELETE FROM TRACK_TIMELINE_TITLE_GUESS WHERE TRACK_TIMELINE_GAME_ID = ? AND CARD_ID = ?", gameId, cardId)
}

// ClearGuesses empties the round's guesses and its list of skipped songs, ready
// for the next round.
func ClearGuesses(gameId uuid.UUID) error {
	if err := execute("DELETE FROM TRACK_TIMELINE_TITLE_GUESS WHERE TRACK_TIMELINE_GAME_ID = ?", gameId); err != nil {
		return err
	}
	return execute("DELETE FROM TRACK_TIMELINE_SKIPPED_CARD WHERE TRACK_TIMELINE_GAME_ID = ?", gameId)
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

// PlacementYearRange is the year window a placement slot sits in — the
// neighbouring cards' years, or open-ended when there is no neighbour.
// Snapshotted at lock-in so a steal can re-check whether the original was
// also correct without re-deriving bounds after a mid-window buy.
type PlacementYearRange struct {
	HasLower bool
	Lower    int
	HasUpper bool
	Upper    int
}

// PlacementYearRangeOf returns the year bounds for inserting at position.
func PlacementYearRangeOf(timeline []TimelineCard, position int) PlacementYearRange {
	var r PlacementYearRange
	if position > 0 && position <= len(timeline) {
		r.HasLower = true
		r.Lower = timeline[position-1].ReleaseYear
	}
	if position >= 0 && position < len(timeline) {
		r.HasUpper = true
		r.Upper = timeline[position].ReleaseYear
	}
	return r
}

// Contains reports whether releaseYear falls in this slot, matching
// IsPlacementCorrect (equal neighbour years are allowed on both sides).
func (r PlacementYearRange) Contains(year int) bool {
	if r.HasLower && year < r.Lower {
		return false
	}
	if r.HasUpper && year > r.Upper {
		return false
	}
	return true
}

// Format is a short human label for chat: "any year", "before 1970",
// "after 1989", or "1971–1989".
func (r PlacementYearRange) Format() string {
	switch {
	case !r.HasLower && !r.HasUpper:
		return "any year"
	case !r.HasLower && r.HasUpper:
		return "before " + strconv.Itoa(r.Upper)
	case r.HasLower && !r.HasUpper:
		return "after " + strconv.Itoa(r.Lower)
	default:
		return strconv.Itoa(r.Lower) + "–" + strconv.Itoa(r.Upper)
	}
}

// PositionForYear returns the index a card of the given year would sort into
// within timeline (ascending by ReleaseYear). Used by the exact-year and buy
// actions, neither of which have the player choose a position by hand — the
// year (guessed or, for a purchase, simply known) determines it.
func PositionForYear(timeline []TimelineCard, year int) int {
	for i, card := range timeline {
		if card.ReleaseYear > year {
			return i
		}
	}
	return len(timeline)
}

// AnyEligibleToSteal reports whether any active player besides the one on
// turn holds StealCost tokens, i.e. could claim the steal attempt if a window opened
// right now. Used to decide whether opening the steal-join window is worth
// it at all.
func AnyEligibleToSteal(gameId uuid.UUID) (bool, error) {
	game, err := GetGameById(gameId)
	if err != nil {
		return false, err
	}
	players, err := GetPlayers(gameId)
	if err != nil {
		return false, err
	}
	for _, player := range players {
		if !player.IsActive {
			continue
		}
		if game.CurrentPlayerId.Valid && player.PlayerId == game.CurrentPlayerId.UUID {
			continue
		}
		if player.TokenCount < StealCost {
			continue
		}
		return true, nil
	}
	return false, nil
}

// ClaimSteal attempts to claim the sole steal attempt for this round: an
// atomic UPDATE ... WHERE STEALER_PLAYER_ID IS NULL, so if two players race
// to claim at nearly the same instant, MariaDB's row-level locking serializes
// the two UPDATEs and exactly one can match the WHERE clause. The read-back
// afterward is what tells this specific call whether it was the one that
// matched (there is no RowsAffected available through this codebase's
// execute() wrapper, so this is the substitute) -- by the time it runs, the
// claim (if any) has already been durably decided by the UPDATE, so nobody
// else can have since changed it out from under this read.
//
// A successful claim immediately spends the claimant's StealCost tokens and moves the
// round into PhaseStealTurn: claiming and beginning the turn are the same
// moment now that there is only one steal attempt per round, not a queue to
// wait on.
func ClaimSteal(gameId uuid.UUID, playerId uuid.UUID) (claimed bool, err error) {
	if err := execute(
		"UPDATE TRACK_TIMELINE_GAME SET STEALER_PLAYER_ID = ? WHERE ID = ? AND STEALER_PLAYER_ID IS NULL",
		playerId, gameId,
	); err != nil {
		return false, err
	}

	game, err := GetGameById(gameId)
	if err != nil {
		return false, err
	}
	if !game.StealerPlayerId.Valid || game.StealerPlayerId.UUID != playerId {
		return false, nil
	}

	if _, err := AddPlayerTokens(gameId, playerId, -StealCost); err != nil {
		return true, err
	}
	if err := SetRoundPhase(gameId, PhaseStealTurn); err != nil {
		return true, err
	}
	return true, nil
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

// cardAlreadyOnAnyTimeline reports whether CARD_ID already sits on some seat
// in this game — the invariant GAME_CARD_UNIQUE enforces, checked in Go too
// so a duplicate award becomes ErrRoundAlreadyResolved instead of a SQL error.
func cardAlreadyOnAnyTimeline(gameId uuid.UUID, cardId uuid.UUID) (bool, error) {
	rows, err := query(
		`SELECT 1 FROM TRACK_TIMELINE_PLAYER_TIMELINE WHERE TRACK_TIMELINE_GAME_ID = ? AND CARD_ID = ? LIMIT 1`,
		gameId, cardId,
	)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), nil
}

// The token economy, in whole tokens. A guess pays GuessTokensPerPart for each
// of the title and the artist (so up to twice that per round); everything a
// player can spend tokens on costs a multiple of it, so one perfect guess pays
// for one skip, replay or steal.
const (
	GuessTokensPerPart = 1

	SkipCost    = 2
	ReplayCost  = 2
	NewClipCost = 2
	StealCost   = 2
	BuyCardCost = 10
)

// Economy is the token economy as data, so templates and clients read the same
// numbers the server enforces instead of repeating them as literals. Every game
// uses CurrentEconomy today; if prices ever become a per-lobby setting, this is
// the one place that changes.
type Economy struct {
	GuessTokensPerPart int
	// MaxGuessTokens is the most one guess can pay: the title and the artist.
	MaxGuessTokens int
	SkipCost       int
	ReplayCost     int
	NewClipCost    int
	StealCost      int
	BuyCardCost    int

	MinStartingTokens     int
	MaxStartingTokens     int
	DefaultStartingTokens int
}

// CurrentEconomy is the economy every game plays under.
func CurrentEconomy() Economy {
	return Economy{
		GuessTokensPerPart:    GuessTokensPerPart,
		MaxGuessTokens:        2 * GuessTokensPerPart,
		SkipCost:              SkipCost,
		ReplayCost:            ReplayCost,
		NewClipCost:           NewClipCost,
		StealCost:             StealCost,
		BuyCardCost:           BuyCardCost,
		MinStartingTokens:     MinStartingTokens,
		MaxStartingTokens:     MaxStartingTokens,
		DefaultStartingTokens: DefaultStartingTokens,
	}
}

// Tokens phrases a count for display: "1 token", "2 tokens".
func (Economy) Tokens(n int) string {
	if n == 1 {
		return "1 token"
	}
	return fmt.Sprintf("%d tokens", n)
}

// CanBuyCard reports whether buying a free card is allowed for this seat:
// enough tokens, the purchase would not be the winning song (wins must come
// from a real placement or steal), and the buyer is not in or tied for the
// lead — buying is a catch-up mechanic, so only a player strictly behind
// someone may use it.
func CanBuyCard(timelineLen, tokenCount, cardsToWin int, inLead bool) bool {
	if tokenCount < BuyCardCost {
		return false
	}
	if timelineLen+1 >= cardsToWin {
		return false
	}
	if inLead {
		return false
	}
	return true
}

// HasOrSharesLead reports whether a timeline of myLen cards is at least as long
// as the longest of everyone else's (maxOtherLen) — outright leading or tied
// for first both count. A player alone in the game has no one ahead of them.
func HasOrSharesLead(myLen, maxOtherLen int) bool {
	return myLen >= maxOtherLen
}

// IsInOrTiedForLead reports whether playerId's timeline is as long as or longer
// than every other active player's.
func IsInOrTiedForLead(gameId uuid.UUID, playerId uuid.UUID, timelineLen int) (bool, error) {
	players, err := GetPlayers(gameId)
	if err != nil {
		return false, err
	}
	maxOther := -1
	for _, p := range players {
		if !p.IsActive || p.PlayerId == playerId {
			continue
		}
		if p.TimelineSize > maxOther {
			maxOther = p.TimelineSize
		}
	}
	return HasOrSharesLead(timelineLen, maxOther), nil
}

// BoughtCard is what a successful BuyCard produced — enough to announce it,
// nothing more. Unlike RoundOutcome this never drives a reveal: buying is a
// private transaction between one player and their own token balance, not an
// outcome of the round in progress.
type BoughtCard struct {
	CardId      uuid.UUID
	Title       string
	Artist      string
	ReleaseYear int
}

// drawnPileCard is one undrawn card pulled directly from the draw pile,
// outside of TRACK_TIMELINE_CURRENT_CARD — the shared "song in play" slot
// that the turn player's own round revolves around.
type drawnPileCard struct {
	CardId      uuid.UUID
	Title       string
	Artist      string
	ReleaseYear int
}

// drawFromPile pulls the next undrawn card (lowest SHUFFLE_ORDER) straight off
// the pile and marks it drawn, without touching TRACK_TIMELINE_CURRENT_CARD.
// CardId is uuid.Nil if the pile has nothing left to draw. Broken-video cards
// never reach this query in the first place — they're excluded when the pile
// is built (and pruned again on StartGame if stale), the same guarantee
// ordinary turn draws already rely on.
func drawFromPile(gameId uuid.UUID) (drawnPileCard, error) {
	var card drawnPileCard

	sqlString := `
		SELECT DP.CARD_ID, C.TITLE, C.ARTIST, DP.RELEASE_YEAR
		FROM TRACK_TIMELINE_DRAW_PILE DP
			INNER JOIN CARD C ON C.ID = DP.CARD_ID
		WHERE DP.TRACK_TIMELINE_GAME_ID = ? AND DP.DRAWN = 0
		ORDER BY DP.SHUFFLE_ORDER ASC, DP.ID ASC
		LIMIT 1
	`
	rows, err := query(sqlString, gameId)
	if err != nil {
		return card, err
	}
	defer rows.Close()

	for rows.Next() {
		if err := rows.Scan(&card.CardId, &card.Title, &card.Artist, &card.ReleaseYear); err != nil {
			log.Println(err)
			return card, errors.New("failed to scan row in query results")
		}
	}
	if card.CardId == uuid.Nil {
		return card, nil
	}

	sqlMark := "UPDATE TRACK_TIMELINE_DRAW_PILE SET DRAWN = 1 WHERE TRACK_TIMELINE_GAME_ID = ? AND CARD_ID = ?"
	if err := execute(sqlMark, gameId, card.CardId); err != nil {
		return card, err
	}

	return card, nil
}

// BuyCard draws a fresh card straight from the top of the draw pile — never
// whatever card is currently queued up for someone else's turn — and inserts
// it into the buyer's own timeline at its correct position, at the cost of
// BuyCardCost tokens, with no listen and no guess. Available to any player,
// any time the phase allows it (the caller gates on RoundPhase); this never
// touches the round already in progress, so it is safe to call regardless of
// whose turn it is.
//
// Refuses the purchase if it would be the card that reaches CardsToWin: a win
// has to come from a real guess, not a purchase.
func BuyCard(gameId uuid.UUID, playerId uuid.UUID) (BoughtCard, error) {
	var bought BoughtCard

	game, err := GetGameById(gameId)
	if err != nil {
		return bought, err
	}

	timeline, err := GetPlayerTimeline(gameId, playerId)
	if err != nil {
		return bought, err
	}
	tokens, err := GetPlayerTokens(gameId, playerId)
	if err != nil {
		return bought, err
	}
	inLead, err := IsInOrTiedForLead(gameId, playerId, len(timeline))
	if err != nil {
		return bought, err
	}
	if !CanBuyCard(len(timeline), tokens, game.CardsToWin, inLead) {
		if tokens < BuyCardCost {
			return bought, errors.New("not enough tokens")
		}
		if inLead {
			return bought, errors.New("you can't buy a card while you're in or tied for the lead")
		}
		return bought, errors.New("that would be the winning card — it has to come from a real guess")
	}

	card, err := drawFromPile(gameId)
	if err != nil {
		return bought, err
	}
	if card.CardId == uuid.Nil {
		return bought, errors.New("the draw pile is empty")
	}
	bought.CardId = card.CardId
	bought.Title = card.Title
	bought.Artist = card.Artist
	bought.ReleaseYear = card.ReleaseYear

	position := PositionForYear(timeline, card.ReleaseYear)
	if err := insertIntoTimeline(gameId, playerId, card.CardId, card.ReleaseYear, position); err != nil {
		return bought, err
	}
	if _, err := AddPlayerTokens(gameId, playerId, -BuyCardCost); err != nil {
		return bought, err
	}

	if logErr := LogCardEvent(card.CardId, CardEventBought); logErr != nil {
		log.Println(logErr)
	}

	return bought, nil
}

// RoundOutcome is what the reveal produced, for the announcement and the popup.
type RoundOutcome struct {
	CardId      uuid.UUID
	Title       string
	Artist      string
	ReleaseYear int

	// CurrentPlayerId / CurrentPlayerName is who was on turn, and
	// CurrentPlayerCorrect whether their own placement stood up.
	CurrentPlayerId      uuid.NullUUID
	CurrentPlayerName    string
	CurrentPlayerCorrect bool

	// WinnerPlayerId is who actually took the card. Invalid when nobody did.
	WinnerPlayerId uuid.NullUUID
	WinnerName     string
	WonByChallenge bool

	// GuessTokenWinners is every guess that earned its player a title/artist
	// guess token this round (paid when judged, announced at reveal),
	// independent of who (if anyone) won the card.
	// Every qualifying guess is its own winner -- there is no single-token
	// race. Empty when nobody qualified under the lobby's guess mode.
	GuessTokenWinners []Guess

	// Guesses is every guess submitted this round, oldest first. Announcing
	// these to chat is deferred until reveal (unlike the exact-year wager,
	// there is no separate per-guess field to leak early) so nobody watching
	// chat can read off who nailed the title/artist before the song is
	// actually revealed.
	Guesses []Guess

	// Exact-year wager on the turn player's placement, if any. Announced at
	// reveal so the steal window is not spoiled by the year digits.
	HasExactYearGuess bool
	ExactYearGuess    int
	ExactYearCorrect  bool
	YearWager         int
	ExactYearPlayer   string

	// CurrentPlayerRange is where the turn player put the song ("1989–2002",
	// "before 1970"), for the result popup. Empty when they never placed.
	CurrentPlayerRange string

	// Populated on a successful steal so chat can show both year windows
	// alongside the real year.
	OriginalPlayerName string
	OriginalRangeLabel string
	StealerRangeLabel  string
}

// ErrRoundAlreadyResolved means another goroutine (steal attempt vs. scheduled
// timeout) already finished this round. Callers must not announce again.
var ErrRoundAlreadyResolved = errors.New("round already resolved")

// roundResolveMu serializes resolveRound within this process so a steal
// attempt and its steal-turn timeout cannot both award the same card (the
// timeline unique key is per-player, so without this both players can end up
// holding the same song).
var roundResolveMu sync.Mutex

// resolveRound is the common tail of every way a round can end: award the
// card to winnerPlayerId at winPosition (or, when winnerPlayerId is
// uuid.Nil, award nobody), run the independent guess-token economy, clear
// all round-scoped state, and move the phase to reveal.
//
// Per-attempt placement stats (LogPlacement) are logged by the caller at the
// moment each attempt is judged — the turn player's own in PlaceCard, a
// stealer's in AttemptSteal — not batched here, since attempts are now
// judged one at a time as they happen rather than gathered and judged
// together at the end.
//
// It does not advance the turn or draw the next song — the caller does that
// after it has also checked for a game winner, so a won game stops on the
// reveal instead of flicking straight to the next round.
func resolveRound(gameId uuid.UUID, winnerPlayerId uuid.UUID, winnerName string, winPosition int, wonBySteal bool) (RoundOutcome, error) {
	roundResolveMu.Lock()
	defer roundResolveMu.Unlock()

	var outcome RoundOutcome

	game, err := GetGameById(gameId)
	if err != nil {
		return outcome, err
	}
	// A concurrent resolver already moved us to reveal (or the game ended).
	if game.RoundPhase == PhaseReveal || game.GameStatus == StatusFinished {
		return outcome, ErrRoundAlreadyResolved
	}

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

	// Capture the exact-year wager before ClearPlacements so reveal chat can
	// announce it without having leaked the digits during the steal window.
	if placement, err := GetPlacement(gameId); err == nil && placement.Id != uuid.Nil && placement.ExactYearGuess.Valid {
		outcome.HasExactYearGuess = true
		outcome.ExactYearGuess = int(placement.ExactYearGuess.Int64)
		outcome.ExactYearCorrect = outcome.ExactYearGuess == card.ReleaseYear
		outcome.YearWager = placement.YearWager
		outcome.ExactYearPlayer = placement.PlayerName
	}
	if placement, err := GetPlacement(gameId); err == nil && placement.Id != uuid.Nil {
		outcome.CurrentPlayerRange = placement.YearRange.Format()
	}

	if game.CurrentPlayerId.Valid {
		outcome.CurrentPlayerId = game.CurrentPlayerId
		players, err := GetPlayers(gameId)
		if err == nil {
			for _, p := range players {
				if p.PlayerId == game.CurrentPlayerId.UUID {
					outcome.CurrentPlayerName = p.UserName
					break
				}
			}
		}
	}

	if winnerPlayerId != uuid.Nil {
		// Belt-and-suspenders with GAME_CARD_UNIQUE: if this card somehow
		// already sits on anyone's timeline (a prior partial resolve), do not
		// award it again — that was how both seats ended up holding the song.
		alreadyHeld, holdErr := cardAlreadyOnAnyTimeline(gameId, card.CardId)
		if holdErr != nil {
			return outcome, holdErr
		}
		if alreadyHeld {
			return outcome, ErrRoundAlreadyResolved
		}
		if err := insertIntoTimeline(gameId, winnerPlayerId, card.CardId, card.ReleaseYear, winPosition); err != nil {
			return outcome, err
		}
		outcome.WinnerPlayerId = uuid.NullUUID{UUID: winnerPlayerId, Valid: true}
		outcome.WinnerName = winnerName
		outcome.WonByChallenge = wonBySteal
		outcome.CurrentPlayerCorrect = !wonBySteal
	} else {
		if logErr := LogCardEvent(card.CardId, CardEventDiscarded); logErr != nil {
			log.Println(logErr)
		}
	}

	// Snapshotted before ClearGuesses below so the caller can announce every
	// guess to chat now that the round is actually over.
	if guesses, guessErr := GetGuesses(gameId); guessErr == nil {
		outcome.Guesses = guesses
	} else {
		log.Println(guessErr)
	}

	// The guess-token economy is independent of the card economy above. The
	// tokens were already paid when each guess was judged; this only collects
	// who earned one so the reveal can announce it.
	guessWinners, err := GuessTokenWinners(gameId)
	if err != nil {
		return outcome, err
	}
	outcome.GuessTokenWinners = guessWinners

	if err := ClearPlacements(gameId); err != nil {
		return outcome, err
	}
	if err := ClearGuesses(gameId); err != nil {
		return outcome, err
	}
	if err := SetRoundPhase(gameId, PhaseReveal); err != nil {
		return outcome, err
	}

	return outcome, nil
}

// judgeBeforeResolving judges the round's guesses ahead of resolveRound, which
// snapshots them for the reveal. Deliberately not inside resolveRound: that holds
// a process-wide lock, and judging waits on the network. A failure is logged and
// the round resolves anyway, since the round must end either way.
func judgeBeforeResolving(gameId uuid.UUID) {
	if err := JudgeRoundGuesses(gameId); err != nil {
		log.Println(err)
	}
}

// ResolveRoundWon awards the card to winnerPlayerId at winPosition.
// wonBySteal distinguishes a stealer's win from the turn player's own
// correct placement, for the announcement and the reveal popup.
func ResolveRoundWon(gameId uuid.UUID, winnerPlayerId uuid.UUID, winnerName string, winPosition int, wonBySteal bool) (RoundOutcome, error) {
	judgeBeforeResolving(gameId)
	return resolveRound(gameId, winnerPlayerId, winnerName, winPosition, wonBySteal)
}

// ResolveRoundFallbackToOriginal re-judges the turn player's own original
// placement and resolves accordingly. Called whenever nobody's active steal
// attempt settled the round in the stealer's favor: nobody was eligible to
// steal in the first place, the steal-join window closed with nobody
// claiming it, a claimed steal attempt was wrong, or a steal turn timed out.
//
// The original placement's correctness is deliberately not judged or
// revealed until this point (see PlaceCard): a stealer who could only ever
// be invited once the original was already known to be wrong would be
// taking no real risk. This is where that suspense resolves — if the
// original was actually right all along, the turn player keeps the card
// (the stealer, if there was one, already spent their token finding out);
// otherwise the card is discarded.
func ResolveRoundFallbackToOriginal(gameId uuid.UUID) (RoundOutcome, error) {
	judgeBeforeResolving(gameId)
	placement, err := GetPlacement(gameId)
	if err != nil {
		return RoundOutcome{}, err
	}
	if placement.Id == uuid.Nil {
		return RoundOutcome{}, errors.New("no placement to fall back to")
	}

	card, err := GetCurrentCardAnswer(gameId)
	if err != nil {
		return RoundOutcome{}, err
	}
	timeline, err := GetPlayerTimeline(gameId, placement.PlayerId)
	if err != nil {
		return RoundOutcome{}, err
	}

	if !IsPlacementCorrect(timeline, placement.Position, card.ReleaseYear) {
		return resolveRound(gameId, uuid.Nil, "", 0, false)
	}
	return resolveRound(gameId, placement.PlayerId, placement.PlayerName, placement.Position, false)
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
	// The paid replay is per-round, so the incoming player starts with theirs
	// unspent regardless of what the outgoing one did.
	if err := SetReplayUsed(gameId, false); err != nil {
		return err
	}
	if err := SetRoundPhase(gameId, PhaseListening); err != nil {
		return err
	}
	// The round that just ended is over and the next song has not started: the
	// window in which a challenge about it can be raised.
	if err := SetBetweenRounds(gameId, true); err != nil {
		return err
	}

	return DrawCard(gameId)
}

// SkipCurrentCard discards the song in play without judging anyone and draws a
// replacement for the same player. This is the escape hatch for a video that is
// dead, region-locked, or simply will not load — without it one bad link wedges
// the round for everyone.
//
// keepGuesses says what becomes of the guesses made on the abandoned song. True
// is an ordinary skip: the song was heard and played, so its guesses stay on file
// against it and are judged, and paid, with the rest of the round. False is for a
// song that never really played (a dead video), where judging them would be
// unfair, so they are dropped.
func SkipCurrentCard(gameId uuid.UUID, keepGuesses bool) error {
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

	// Placements belong to the abandoned song and are dropped. Its guesses are
	// not: they stay against it, and the round is judged song by song.
	if err := ClearPlacements(gameId); err != nil {
		return err
	}
	if keepGuesses {
		if err := AddSkippedCard(gameId, card.CardId); err != nil {
			return err
		}
	} else if err := DiscardGuessesForCard(gameId, card.CardId); err != nil {
		return err
	}
	// The replacement song has not been heard at all yet, so a replay already
	// spent on the abandoned song must not carry over and hide the Restart
	// button (or block the token spend) for a clip nobody has used it on.
	if err := SetReplayUsed(gameId, false); err != nil {
		return err
	}
	if err := SetRoundPhase(gameId, PhaseListening); err != nil {
		return err
	}

	return DrawCard(gameId)
}
