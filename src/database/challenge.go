package database

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

// A challenge is a player's claim that the game wronged them, put to the other
// players for a vote (see TRACK_TIMELINE_CHALLENGE.sql). Everything the rules
// say about who may raise one and how a vote is counted lives here; the
// handlers in api/tracktimeline only translate it to and from HTTP.

const (
	ChallengeKindCard   = "card"
	ChallengeKindTokens = "tokens"

	ChallengeStatusOpen      = "open"
	ChallengeStatusUpheld    = "upheld"
	ChallengeStatusRejected  = "rejected"
	ChallengeStatusWithdrawn = "withdrawn"
	ChallengeStatusVoid      = "void"

	// MaxChallengeTokens caps what a token claim can ask for, so the one
	// number a player types cannot be an absurd one the table feels obliged to
	// argue about. It matches the price of a bought card.
	MaxChallengeTokens = BuyCardCost

	MinChallengeReasonLength = 3
	MaxChallengeReasonLength = 300

	// ChallengeVoteWindow is how long the table has to vote before the votes
	// cast so far decide it. Without a limit one player who walked away would
	// freeze the whole game, since nothing else can happen while it is open.
	ChallengeVoteWindow = 60 * time.Second
)

var (
	ErrChallengeAlreadyOpen = errors.New("a challenge is already open")
	ErrAlreadyVoted         = errors.New("you have already voted on this challenge")
)

// Challenge is one row of TRACK_TIMELINE_CHALLENGE with the challenger's name.
type Challenge struct {
	Id            uuid.UUID
	CreatedOnDate time.Time
	GameId        uuid.UUID
	PlayerId      uuid.UUID
	PlayerName    string
	Kind          string
	Tokens        int
	Reason        string
	Status        string
}

// Exists reports whether this is a real row rather than the zero value that
// "no such challenge" is returned as.
func (c Challenge) Exists() bool {
	return c.Id != uuid.Nil
}

// ChallengeVerdict decides a vote. A strict majority of the eligible voters
// (everyone but the challenger) upholds it. It is rejected as soon as a
// majority has become impossible, rather than waiting for the rest to vote, and
// at the deadline whatever has not been upheld by then is rejected: a player
// who never voted is not a yes.
func ChallengeVerdict(agree, disagree, eligible int, timedOut bool) (resolved bool, upheld bool) {
	needed := eligible/2 + 1
	if agree >= needed {
		return true, true
	}
	if disagree > eligible-needed {
		return true, false
	}
	if timedOut {
		return true, false
	}
	return false, false
}

// SetBetweenRounds marks whether the game is between rounds: a round has
// resolved and the next song has not been started. A challenge may only be
// raised while it is.
func SetBetweenRounds(gameId uuid.UUID, between bool) error {
	return execute("UPDATE TRACK_TIMELINE_GAME SET BETWEEN_ROUNDS = ? WHERE ID = ?", between, gameId)
}

const challengeColumns = `
	C.ID, C.CREATED_ON_DATE, C.TRACK_TIMELINE_GAME_ID, C.PLAYER_ID, U.NAME,
	C.KIND, C.TOKENS, C.REASON, C.STATUS
`

const challengeJoins = `
	FROM TRACK_TIMELINE_CHALLENGE C
		INNER JOIN PLAYER P ON P.ID = C.PLAYER_ID
		INNER JOIN USER U ON U.ID = P.USER_ID
`

func scanChallenges(sqlString string, params ...any) ([]Challenge, error) {
	rows, err := query(sqlString, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var challenges []Challenge
	for rows.Next() {
		var c Challenge
		if err := rows.Scan(
			&c.Id, &c.CreatedOnDate, &c.GameId, &c.PlayerId, &c.PlayerName,
			&c.Kind, &c.Tokens, &c.Reason, &c.Status,
		); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		challenges = append(challenges, c)
	}
	return challenges, nil
}

// GetOpenChallenge returns the game's open challenge, or the zero Challenge
// when nothing is being voted on.
func GetOpenChallenge(gameId uuid.UUID) (Challenge, error) {
	challenges, err := scanChallenges(
		"SELECT "+challengeColumns+challengeJoins+" WHERE C.TRACK_TIMELINE_GAME_ID = ? AND C.STATUS = 'open'",
		gameId,
	)
	if err != nil || len(challenges) == 0 {
		return Challenge{}, err
	}
	return challenges[0], nil
}

// GetChallenge returns one challenge by id, or the zero Challenge.
func GetChallenge(challengeId uuid.UUID) (Challenge, error) {
	challenges, err := scanChallenges(
		"SELECT "+challengeColumns+challengeJoins+" WHERE C.ID = ?",
		challengeId,
	)
	if err != nil || len(challenges) == 0 {
		return Challenge{}, err
	}
	return challenges[0], nil
}

// PlayerHasChallengeLeft reports whether a player may still raise a challenge:
// they get one, and keep it for as long as theirs are upheld, but a rejected
// one uses it up for the rest of the game.
func PlayerHasChallengeLeft(gameId uuid.UUID, playerId uuid.UUID) (bool, error) {
	rows, err := query(
		"SELECT COUNT(*) FROM TRACK_TIMELINE_CHALLENGE WHERE TRACK_TIMELINE_GAME_ID = ? AND PLAYER_ID = ? AND STATUS = 'rejected'",
		gameId, playerId,
	)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	var rejected int
	for rows.Next() {
		if err := rows.Scan(&rejected); err != nil {
			log.Println(err)
			return false, errors.New("failed to scan row in query results")
		}
	}
	return rejected == 0, nil
}

// ChallengeVoters are the players who get a say: everyone active except the
// challenger.
func ChallengeVoters(gameId uuid.UUID, challengerId uuid.UUID) ([]Player, error) {
	players, err := GetPlayers(gameId)
	if err != nil {
		return nil, err
	}
	voters := make([]Player, 0, len(players))
	for _, p := range players {
		if p.IsActive && p.PlayerId != challengerId {
			voters = append(voters, p)
		}
	}
	return voters, nil
}

// CreateChallenge opens a challenge. The row's unique OPEN_GAME_ID is what
// refuses a second one while the first is still open, however the two requests
// interleave. The creation time is Go's own clock, passed in, so the vote
// deadline is computed against the same clock the server's timeout uses.
func CreateChallenge(gameId uuid.UUID, playerId uuid.UUID, kind string, tokens int, reason string) (Challenge, error) {
	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return Challenge{}, errors.New("failed to generate new id")
	}

	err = execute(`
		INSERT INTO TRACK_TIMELINE_CHALLENGE
			(ID, CREATED_ON_DATE, TRACK_TIMELINE_GAME_ID, PLAYER_ID, KIND, TOKENS, REASON, STATUS, OPEN_GAME_ID)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'open', ?)
	`, id, time.Now(), gameId, playerId, kind, tokens, reason, gameId)
	if err != nil {
		if open, openErr := GetOpenChallenge(gameId); openErr == nil && open.Exists() {
			return Challenge{}, ErrChallengeAlreadyOpen
		}
		return Challenge{}, err
	}
	return GetChallenge(id)
}

// CastChallengeVote records one player's vote. A second vote from the same
// player is refused rather than overwriting the first.
func CastChallengeVote(challengeId uuid.UUID, playerId uuid.UUID, agree bool) error {
	rows, err := query(
		"SELECT COUNT(*) FROM TRACK_TIMELINE_CHALLENGE_VOTE WHERE CHALLENGE_ID = ? AND PLAYER_ID = ?",
		challengeId, playerId,
	)
	if err != nil {
		return err
	}
	var existing int
	for rows.Next() {
		if err := rows.Scan(&existing); err != nil {
			rows.Close()
			log.Println(err)
			return errors.New("failed to scan row in query results")
		}
	}
	rows.Close()
	if existing > 0 {
		return ErrAlreadyVoted
	}

	return execute(
		"INSERT INTO TRACK_TIMELINE_CHALLENGE_VOTE (ID, CHALLENGE_ID, PLAYER_ID, AGREE) VALUES (UUID(), ?, ?, ?)",
		challengeId, playerId, agree,
	)
}

// ChallengeVotes returns each voter's vote on a challenge, keyed by player.
func ChallengeVotes(challengeId uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := query("SELECT PLAYER_ID, AGREE FROM TRACK_TIMELINE_CHALLENGE_VOTE WHERE CHALLENGE_ID = ?", challengeId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	votes := make(map[uuid.UUID]bool)
	for rows.Next() {
		var playerId uuid.UUID
		var agree bool
		if err := rows.Scan(&playerId, &agree); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		votes[playerId] = agree
	}
	return votes, nil
}

// ChallengeTally counts the votes of the players still eligible to vote, so a
// vote from someone who has since left does not count.
func ChallengeTally(votes map[uuid.UUID]bool, voters []Player) (agree, disagree int) {
	for _, voter := range voters {
		if vote, voted := votes[voter.PlayerId]; voted {
			if vote {
				agree++
			} else {
				disagree++
			}
		}
	}
	return agree, disagree
}

// CloseChallenge settles an open challenge with a final status and frees the
// game to have another. It reports false, changing nothing, when the challenge
// was already closed — the vote, the timeout and a withdrawal can all race to
// close the same one, and only the first may pay out.
func CloseChallenge(challengeId uuid.UUID, status string) (bool, error) {
	closeMu.Lock()
	defer closeMu.Unlock()

	challenge, err := GetChallenge(challengeId)
	if err != nil {
		return false, err
	}
	if !challenge.Exists() || challenge.Status != ChallengeStatusOpen {
		return false, nil
	}
	err = execute(
		"UPDATE TRACK_TIMELINE_CHALLENGE SET STATUS = ?, OPEN_GAME_ID = NULL WHERE ID = ? AND STATUS = 'open'",
		status, challengeId,
	)
	return err == nil, err
}

var closeMu sync.Mutex

// ErrChallengeCardWouldWin is returned when a card claim could not be paid
// because the card would take the player to the winning count. A win has to come
// from a real placement, the same rule that stops a bought card winning.
var ErrChallengeCardWouldWin = errors.New("that card would win the game, so it cannot be granted")

// GrantChallengeCard gives a player a free card drawn from the top of the draw
// pile, placed in its correct spot on their timeline. It is BuyCard without the
// price, and with the same refusal to hand out the winning card.
func GrantChallengeCard(gameId uuid.UUID, playerId uuid.UUID) (BoughtCard, error) {
	var granted BoughtCard

	game, err := GetGameById(gameId)
	if err != nil {
		return granted, err
	}
	timeline, err := GetPlayerTimeline(gameId, playerId)
	if err != nil {
		return granted, err
	}
	if len(timeline)+1 >= game.CardsToWin {
		return granted, ErrChallengeCardWouldWin
	}

	card, err := drawFromPile(gameId)
	if err != nil {
		return granted, err
	}
	if card.CardId == uuid.Nil {
		return granted, errors.New("the draw pile is empty")
	}
	granted = BoughtCard{
		CardId:      card.CardId,
		Title:       card.Title,
		Artist:      card.Artist,
		ReleaseYear: card.ReleaseYear,
	}

	position := PositionForYear(timeline, card.ReleaseYear)
	if err := insertIntoTimeline(gameId, playerId, card.CardId, card.ReleaseYear, position); err != nil {
		return granted, err
	}
	if logErr := LogCardEvent(card.CardId, CardEventDealt); logErr != nil {
		log.Println(logErr)
	}
	return granted, nil
}
