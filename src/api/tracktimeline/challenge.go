package apiTrackTimeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	gsWebsocket "github.com/gerp93/gameshell-framework/websocket"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
)

// A challenge is a player saying the game got something wrong against them —
// the AI judge, a glitch, a video that doesn't match its song, a bad year — and
// asking to be paid for it: a free card, or a number of tokens. It can only be
// raised between rounds, the other players vote, and a strict majority of them
// pays it. Like a baseball challenge it can be used again for as long as it
// keeps being upheld; a rejected one uses it up for the rest of the game.
//
// While one is open nothing else in the game may happen (see
// challengeInProgress), and the vote has a deadline so one absent player cannot
// freeze everybody.

// challengeMu serializes the things that can close a challenge — the vote that
// settles it, the deadline, a withdrawal — so only one of them pays out.
var challengeMu sync.Mutex

// challengePayload is the challenge: websocket message and the body of GET
// /challenge. It carries the time left rather than an absolute deadline, for
// the same reason the steal countdown does: a browser's clock may not agree
// with the server's.
type challengePayload struct {
	Id             string `json:"id"`
	ChallengerId   string `json:"challengerId"`
	ChallengerName string `json:"challengerName"`
	Kind           string `json:"kind"`
	Tokens         int    `json:"tokens"`
	Reason         string `json:"reason"`
	RemainingMs    int64  `json:"remainingMs"`
	WindowMs       int64  `json:"windowMs"`
	Eligible       int    `json:"eligible"`
	Voted          int    `json:"voted"`
}

// challengeEndPayload is the challengeEnd: websocket message.
type challengeEndPayload struct {
	Id      string `json:"id"`
	Outcome string `json:"outcome"`
	Message string `json:"message"`
}

func buildChallengePayload(c database.Challenge, voters []database.Player, votes map[uuid.UUID]bool) challengePayload {
	agree, disagree := database.ChallengeTally(votes, voters)
	remaining := time.Until(c.CreatedOnDate.Add(database.ChallengeVoteWindow)).Milliseconds()
	if remaining < 0 {
		remaining = 0
	}
	return challengePayload{
		Id:             c.Id.String(),
		ChallengerId:   c.PlayerId.String(),
		ChallengerName: c.PlayerName,
		Kind:           c.Kind,
		Tokens:         c.Tokens,
		Reason:         c.Reason,
		RemainingMs:    remaining,
		WindowMs:       database.ChallengeVoteWindow.Milliseconds(),
		Eligible:       len(voters),
		Voted:          agree + disagree,
	}
}

func sendChallenge(lobbyId uuid.UUID, payload challengePayload) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		log.Println(err)
		return
	}
	gsWebsocket.LobbyBroadcast(lobbyId, "challenge:"+string(encoded))
}

func sendChallengeEnd(lobbyId uuid.UUID, c database.Challenge, outcome, message string) {
	encoded, err := json.Marshal(challengeEndPayload{Id: c.Id.String(), Outcome: outcome, Message: message})
	if err != nil {
		log.Println(err)
		return
	}
	gsWebsocket.LobbyBroadcast(lobbyId, "challengeEnd:"+string(encoded))
}

// challengeInProgress refuses a request, and reports true, while a challenge is
// being voted on: nothing else — a new song, a guess, a placement, a buy —
// happens until the table has decided. Every handler that changes the game
// calls it straight after loadContext.
func challengeInProgress(w http.ResponseWriter, ctx gameContext) bool {
	open, err := database.GetOpenChallenge(ctx.Game.Id)
	if err != nil {
		log.Println(err)
		return false
	}
	if !open.Exists() {
		return false
	}
	w.WriteHeader(http.StatusConflict)
	_, _ = w.Write([]byte("A challenge is being voted on — wait for the result."))
	return true
}

// claimText is what a challenge asks for, as chat words.
func claimText(c database.Challenge) string {
	if c.Kind == database.ChallengeKindCard {
		return "a free card"
	}
	return tokenCount(c.Tokens)
}

// OpenChallenge raises a challenge. Between rounds only, one open at a time,
// and only by a player who still has one left.
func OpenChallenge(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	fail := func(message string) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(message))
	}

	if ctx.Game.GameStatus != database.StatusActive {
		fail("The game is not running.")
		return
	}
	if !ctx.Game.BetweenRounds || ctx.Game.RoundPhase != database.PhaseListening {
		fail("A challenge can only be raised between rounds, before the next song starts.")
		return
	}
	if open, err := database.GetOpenChallenge(ctx.Game.Id); err == nil && open.Exists() {
		fail("A challenge is already being voted on.")
		return
	}
	if left, err := database.PlayerHasChallengeLeft(ctx.Game.Id, ctx.Player.Id); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check your challenges."))
		return
	} else if !left {
		fail("You are out of challenges.")
		return
	}

	voters, err := database.ChallengeVoters(ctx.Game.Id, ctx.Player.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to find the voters."))
		return
	}
	if len(voters) == 0 {
		fail("There is nobody else to vote on it.")
		return
	}

	kind := r.FormValue("kind")
	tokens := 0
	switch kind {
	case database.ChallengeKindCard:
		timeline, err := database.GetPlayerTimeline(ctx.Game.Id, ctx.Player.Id)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to read your timeline."))
			return
		}
		if len(timeline)+1 >= ctx.Game.CardsToWin {
			fail("A card would win you the game, so it can't be claimed — ask for tokens instead.")
			return
		}
	case database.ChallengeKindTokens:
		tokens, err = strconv.Atoi(strings.TrimSpace(r.FormValue("tokens")))
		if err != nil || tokens < 1 || tokens > database.MaxChallengeTokens {
			fail(fmt.Sprintf("Ask for between 1 and %d tokens.", database.MaxChallengeTokens))
			return
		}
	default:
		fail("Choose whether you are owed a card or tokens.")
		return
	}

	reason := strings.TrimSpace(r.FormValue("reason"))
	if utf8.RuneCountInString(reason) < database.MinChallengeReasonLength {
		fail("Say what went wrong so the others can vote on it.")
		return
	}
	reason = truncateRunes(reason, database.MaxChallengeReasonLength)

	challenge, err := database.CreateChallenge(ctx.Game.Id, ctx.Player.Id, kind, tokens, reason)
	if err != nil {
		if errors.Is(err, database.ErrChallengeAlreadyOpen) {
			fail("A challenge is already being voted on.")
			return
		}
		log.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to open the challenge."))
		return
	}

	// The next song may have been started between the checks above and the
	// insert. A challenge must never be open over a song that is playing.
	if fresh, err := database.GetGameById(ctx.Game.Id); err == nil && !fresh.BetweenRounds {
		_, _ = database.CloseChallenge(challenge.Id, database.ChallengeStatusVoid)
		fail("The next song has already started.")
		return
	}

	announce(ctx.LobbyId, fmt.Sprintf("<blue>%s</> raised a challenge — wants %s: “%s”",
		esc(challenge.PlayerName), claimText(challenge), esc(challenge.Reason)))
	sendChallenge(ctx.LobbyId, buildChallengePayload(challenge, voters, nil))

	lobbyId, challengeId := ctx.LobbyId, challenge.Id
	time.AfterFunc(database.ChallengeVoteWindow, func() {
		settleChallenge(lobbyId, challengeId, true)
	})

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Challenge raised."))
}

// VoteChallenge records the caller's vote on the open challenge and settles it
// the moment the result is decided.
func VoteChallenge(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	fail := func(message string) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(message))
	}

	open, err := database.GetOpenChallenge(ctx.Game.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to read the challenge."))
		return
	}
	if !open.Exists() {
		fail("There is no challenge to vote on.")
		return
	}
	if open.PlayerId == ctx.Player.Id {
		fail("You can't vote on your own challenge.")
		return
	}
	voters, err := database.ChallengeVoters(ctx.Game.Id, open.PlayerId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to find the voters."))
		return
	}
	eligible := false
	for _, voter := range voters {
		if voter.PlayerId == ctx.Player.Id {
			eligible = true
			break
		}
	}
	if !eligible {
		fail("You can't vote on this challenge.")
		return
	}

	agree := r.FormValue("agree") == "1"
	if err := database.CastChallengeVote(open.Id, ctx.Player.Id, agree); err != nil {
		if errors.Is(err, database.ErrAlreadyVoted) {
			fail("You have already voted.")
			return
		}
		log.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to record your vote."))
		return
	}

	settleChallenge(ctx.LobbyId, open.Id, false)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Vote recorded."))
}

// WithdrawChallenge lets the challenger take their own challenge back. It
// costs them nothing, so a challenge they have talked themselves out of does
// not use up their one.
func WithdrawChallenge(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}

	challengeMu.Lock()
	defer challengeMu.Unlock()

	open, err := database.GetOpenChallenge(ctx.Game.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to read the challenge."))
		return
	}
	if !open.Exists() {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("There is no challenge to withdraw."))
		return
	}
	if open.PlayerId != ctx.Player.Id {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Only the player who raised it can withdraw it."))
		return
	}

	if changed, err := database.CloseChallenge(open.Id, database.ChallengeStatusWithdrawn); err != nil || !changed {
		if err != nil {
			log.Println(err)
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to withdraw the challenge."))
		return
	}

	message := fmt.Sprintf("%s withdrew their challenge.", open.PlayerName)
	announce(ctx.LobbyId, fmt.Sprintf("<blue>%s</> withdrew their challenge", esc(open.PlayerName)))
	sendChallengeEnd(ctx.LobbyId, open, database.ChallengeStatusWithdrawn, message)
	refresh(ctx.LobbyId)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Withdrawn."))
}

// GetChallenge tells a browser whether a challenge is open and what the caller
// has done about it, so a page that loads mid-vote shows it.
func GetChallenge(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}

	type response struct {
		Open      bool              `json:"open"`
		Challenge *challengePayload `json:"challenge,omitempty"`
		MyVote    string            `json:"myVote,omitempty"`
	}

	open, err := database.GetOpenChallenge(ctx.Game.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to read the challenge."))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if !open.Exists() {
		_ = json.NewEncoder(w).Encode(response{})
		return
	}

	voters, err := database.ChallengeVoters(ctx.Game.Id, open.PlayerId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to find the voters."))
		return
	}
	votes, err := database.ChallengeVotes(open.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to read the votes."))
		return
	}

	payload := buildChallengePayload(open, voters, votes)
	result := response{Open: true, Challenge: &payload}
	if vote, voted := votes[ctx.Player.Id]; voted {
		result.MyVote = "disagree"
		if vote {
			result.MyVote = "agree"
		}
	}
	_ = json.NewEncoder(w).Encode(result)
}

// settleChallenge counts the votes on a challenge and, if that decides it (or
// the deadline has passed), pays it out or rejects it. If it is still open, it
// tells everyone the new tally instead. Safe to call from every path that might
// settle it: only one will find it still open.
func settleChallenge(lobbyId uuid.UUID, challengeId uuid.UUID, timedOut bool) {
	challengeMu.Lock()
	defer challengeMu.Unlock()

	challenge, err := database.GetChallenge(challengeId)
	if err != nil {
		log.Println(err)
		return
	}
	if !challenge.Exists() || challenge.Status != database.ChallengeStatusOpen {
		return
	}

	voters, err := database.ChallengeVoters(challenge.GameId, challenge.PlayerId)
	if err != nil {
		log.Println(err)
		return
	}
	votes, err := database.ChallengeVotes(challenge.Id)
	if err != nil {
		log.Println(err)
		return
	}
	agree, disagree := database.ChallengeTally(votes, voters)

	resolved, upheld := database.ChallengeVerdict(agree, disagree, len(voters), timedOut)
	if !resolved {
		sendChallenge(lobbyId, buildChallengePayload(challenge, voters, votes))
		return
	}

	status, message := finishChallenge(challenge, upheld, agree, disagree)
	if changed, err := database.CloseChallenge(challenge.Id, status); err != nil || !changed {
		if err != nil {
			log.Println(err)
		}
		return
	}

	announce(lobbyId, message.chat)
	sendChallengeEnd(lobbyId, challenge, status, message.plain)
	refresh(lobbyId)
}

type challengeMessage struct {
	chat  string
	plain string
}

// finishChallenge applies a decided challenge — paying it if it was upheld — and
// returns the status to close it with and what to tell the table. A card claim
// that can no longer be paid is voided rather than rejected: the table agreed
// with the player, so it must not cost them their challenge.
func finishChallenge(c database.Challenge, upheld bool, agree, disagree int) (string, challengeMessage) {
	score := fmt.Sprintf("%d–%d", agree, disagree)
	name := c.PlayerName

	if !upheld {
		return database.ChallengeStatusRejected, challengeMessage{
			chat:  fmt.Sprintf("<red>Challenge rejected</> (%s) — %s is out of challenges", score, esc(name)),
			plain: fmt.Sprintf("Challenge rejected (%s). %s is out of challenges.", score, name),
		}
	}

	switch c.Kind {
	case database.ChallengeKindTokens:
		if _, err := database.AddPlayerTokens(c.GameId, c.PlayerId, c.Tokens); err != nil {
			log.Println(err)
			return database.ChallengeStatusVoid, challengeMessage{
				chat:  fmt.Sprintf("<red>Challenge passed (%s) but the tokens could not be paid</>", score),
				plain: fmt.Sprintf("Challenge passed (%s) but the tokens could not be paid.", score),
			}
		}
		return database.ChallengeStatusUpheld, challengeMessage{
			chat:  fmt.Sprintf("<green>Challenge upheld</> (%s) — %s gets %s", score, esc(name), tokenCount(c.Tokens)),
			plain: fmt.Sprintf("Challenge upheld (%s). %s gets %s.", score, name, tokenCount(c.Tokens)),
		}

	case database.ChallengeKindCard:
		granted, err := database.GrantChallengeCard(c.GameId, c.PlayerId)
		if err != nil {
			if !errors.Is(err, database.ErrChallengeCardWouldWin) {
				log.Println(err)
			}
			return database.ChallengeStatusVoid, challengeMessage{
				chat:  fmt.Sprintf("<red>Challenge passed (%s) but the card could not be granted</> — %s keeps their challenge", score, esc(name)),
				plain: fmt.Sprintf("Challenge passed (%s) but the card could not be granted. %s keeps their challenge.", score, name),
			}
		}
		return database.ChallengeStatusUpheld, challengeMessage{
			chat: fmt.Sprintf("<green>Challenge upheld</> (%s) — %s gets a free card: “%s” by %s (%d)",
				score, esc(name), esc(granted.Title), esc(granted.Artist), granted.ReleaseYear),
			plain: fmt.Sprintf("Challenge upheld (%s). %s gets a free card.", score, name),
		}
	}

	return database.ChallengeStatusVoid, challengeMessage{
		chat:  "<red>Challenge could not be settled</>",
		plain: "Challenge could not be settled.",
	}
}
