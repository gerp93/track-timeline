package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	gsAuth "github.com/gerp93/gameshell-framework/auth"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	gsWebsocket "github.com/gerp93/gameshell-framework/websocket"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	apiTrackTimeline "github.com/gerp93/track-timeline/api/tracktimeline"
	"github.com/gerp93/track-timeline/database"
)

// Focused regression tests added after a round of playtesting, covering the
// server-testable behaviors from that pass. Client-only fixes (hx-preserve
// on the guess fields, the steal-turn header-badge clear, the button-height
// CSS, and the 30s listen gate) have no server-side counterpart to test here
// by design (see round.go/track-timeline.js's own doc comments) and were
// instead verified with a live multi-browser Playwright run.

// newPlaytestFixesGame is a smaller, purpose-built setup mirroring
// TestTrackTimelineEndToEnd's own (see setupSchema/players/websocket dial
// there), for tests that don't need that test's full seeded history. namePrefix
// keeps usernames from colliding with other tests sharing the same database.
func newPlaytestFixesGame(t *testing.T, namePrefix string, cardCount, cardsToWin, startingTokens int) (gameId, lobbyId, deckId uuid.UUID, players []*player, srv *httptest.Server) {
	t.Helper()
	dbName := os.Getenv("TRACK_TIMELINE_SQL_DATABASE")
	if !strings.HasPrefix(dbName, "tt_e2e") {
		t.Skipf("refusing to run against %q; set TRACK_TIMELINE_SQL_DATABASE=tt_e2e", dbName)
	}
	gsDatabase.SetEnvVarPrefix("TRACK_TIMELINE")
	gsAuth.SetCookiePrefix("TRACK-TIMELINE")
	if _, err := gsDatabase.CreateDatabaseConnection(); err != nil {
		t.Fatalf("db connect: %v", err)
	}
	setupSchema(t)

	names := []string{namePrefix + "_alice", namePrefix + "_bob", namePrefix + "_carol"}
	for _, n := range names {
		if err := gsDatabase.CreateUser(n, "unused-not-a-login", true); err != nil {
			t.Fatalf("create user %s: %v", n, err)
		}
		id, err := gsDatabase.GetUserIdByName(n)
		if err != nil {
			t.Fatalf("get user %s: %v", n, err)
		}
		players = append(players, &player{name: n, userId: id, received: make(chan string, 256)})
	}

	deckId, err := gsDatabase.CreateDeck(namePrefix+" deck", "", true)
	if err != nil {
		t.Fatalf("create deck: %v", err)
	}
	for i := 0; i < cardCount; i++ {
		year := sql.NullInt64{Int64: int64(1000 + i*10), Valid: true}
		videoId := fmt.Sprintf("%.8sV%03d", namePrefix, i)
		if _, err := database.CreateCard(deckId, videoId,
			fmt.Sprintf("Song %d", i), fmt.Sprintf("Artist %d", i), year, uuid.NullUUID{}); err != nil {
			t.Fatalf("create card %d: %v", i, err)
		}
	}

	lobbyId, err = database.CreateLobby(namePrefix+" lobby", "", "")
	if err != nil {
		t.Fatalf("create lobby: %v", err)
	}
	for _, p := range players {
		if err := gsDatabase.AddUserLobbyAccess(p.userId, lobbyId); err != nil {
			t.Fatalf("grant access: %v", err)
		}
		pid, err := gsDatabase.AddUserToLobby(lobbyId, p.userId)
		if err != nil {
			t.Fatalf("join lobby: %v", err)
		}
		p.playerId = pid
	}
	gameId, err = database.CreateGame(lobbyId, cardsToWin, startingTokens, database.GuessModeBoth, database.DefaultGuessMatchPercent, database.GuessJudgeLocal, database.PlaybackIntro, 20)
	if err != nil {
		t.Fatalf("create game: %v", err)
	}
	if err := database.InitializeDrawPile(gameId, []uuid.UUID{deckId}, nil); err != nil {
		t.Fatalf("init draw pile: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/lobby/{lobbyId}", gsWebsocket.ServeWs)
	srv = httptest.NewServer(mux)

	for _, p := range players {
		rec := httptest.NewRecorder()
		gsAuth.SetUserId(rec, p.userId)
		hdr := http.Header{}
		for _, c := range rec.Result().Cookies() {
			hdr.Add("Cookie", c.Name+"="+c.Value)
		}
		conn, _, err := websocket.DefaultDialer.Dial(
			"ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/lobby/"+lobbyId.String(), hdr)
		if err != nil {
			t.Fatalf("ws dial %s: %v", p.name, err)
		}
		p.conn = conn
		go func(p *player) {
			for {
				_, msg, err := p.conn.ReadMessage()
				if err != nil {
					return
				}
				select {
				case p.received <- string(msg):
				default:
				}
			}
		}(p)
	}
	time.Sleep(300 * time.Millisecond)
	for _, p := range players {
		drain(p)
	}

	if err := database.StartGame(gameId); err != nil {
		t.Fatalf("start game: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	for _, p := range players {
		drain(p)
	}

	return gameId, lobbyId, deckId, players, srv
}

func closePlaytestFixesGame(players []*player, srv *httptest.Server) {
	for _, p := range players {
		_ = p.conn.Close()
	}
	srv.Close()
}

func gamePlayerByUserId(players []*player, id uuid.UUID) *player {
	for _, p := range players {
		if p.userId == id {
			return p
		}
	}
	return nil
}

// TestGuessAnnouncementDeferredUntilReveal guards the fix for guesses
// leaking to lobby chat the instant they were submitted (naming who was
// right about the title/artist before the song was actually revealed): the
// public chat line must not appear until the round resolves, at which point
// every guess submitted that round appears, oldest first.
func TestGuessAnnouncementDeferredUntilReveal(t *testing.T) {
	gameId, lobbyId, _, players, srv := newPlaytestFixesGame(t, "guessdefer", 20, 10, 6)
	defer closePlaytestFixesGame(players, srv)

	game, err := database.GetGameById(gameId)
	if err != nil {
		t.Fatalf("get game: %v", err)
	}
	current := gamePlayerByUserId(players, mustCurrentPlayerUserId(t, gameId))
	var guesser *player
	for _, p := range players {
		if p != current {
			guesser = p
			break
		}
	}

	card, err := database.GetCurrentCardAnswer(gameId)
	if err != nil || card.CardId == uuid.Nil {
		t.Fatalf("current card: %v", err)
	}

	rec := serve(apiTrackTimeline.SubmitGuess, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/guess",
		url.Values{"guessTitle": {card.Title}, "guessArtist": {card.Artist}}, guesser.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("submit guess: %d %s", rec.Code, rec.Body.String())
	}

	msgsBeforeReveal := drainAll(players)
	if chat := chatLines(msgsBeforeReveal); containsSubstring(chat, "guessed") {
		t.Errorf("guess must not be announced to chat before reveal, got %v", chat)
	}

	// Resolve the round with a correct placement, with nobody eligible to
	// steal, so the round resolves immediately and the guess line is free to
	// appear without a steal window's suspense in the way.
	for _, p := range players {
		if p != current {
			if err := database.SetPlayerTokens(gameId, p.playerId, 0); err != nil {
				t.Fatalf("zero tokens: %v", err)
			}
		}
	}
	timeline, err := database.GetPlayerTimeline(gameId, current.playerId)
	if err != nil {
		t.Fatalf("current player timeline: %v", err)
	}
	rec = serve(apiTrackTimeline.PlaceCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/place-card",
		url.Values{"position": {fmt.Sprint(correctPosition(timeline, card.ReleaseYear))}}, current.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("place card: %d %s", rec.Code, rec.Body.String())
	}

	msgsAfterReveal := drainAll(players)
	chatAfter := chatLines(msgsAfterReveal)
	if !containsSubstring(chatAfter, "guessed") {
		t.Errorf("expected the guess to be announced to chat at reveal, got %v", chatAfter)
	}
	if !containsSubstring(chatAfter, card.Title) {
		t.Errorf("expected the guess announcement to quote the guess text, got %v", chatAfter)
	}
	_ = game
}

// TestBuyCardCostAndStrictLeaderRestriction guards two related fixes: the
// buy cost raised from 2 to 3 tokens, and the new rule that a player
// strictly ahead of every other active player cannot buy at all (ties for
// the lead still can).
func TestBuyCardCostAndStrictLeaderRestriction(t *testing.T) {
	gameId, lobbyId, _, players, srv := newPlaytestFixesGame(t, "buylead", 20, 10, 10)
	defer closePlaytestFixesGame(players, srv)

	if database.BuyCardCost != 3 {
		t.Fatalf("expected BuyCardCost to be 3, got %d", database.BuyCardCost)
	}

	leader, rest := players[0], players[1:]
	// Give the leader one bought card up front (nobody is in the lead yet,
	// so this first buy must succeed) to become the strict leader.
	rec := serve(apiTrackTimeline.BuyCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/buy-card", url.Values{}, leader.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("first buy (nobody in the lead yet) should succeed: %d %s", rec.Code, rec.Body.String())
	}
	tokensAfterFirstBuy, err := database.GetPlayerTokens(gameId, leader.playerId)
	if err != nil || tokensAfterFirstBuy != 7 {
		t.Errorf("expected the buy to cost 3 tokens (10 -> 7), got %d (%v)", tokensAfterFirstBuy, err)
	}

	// The leader (now strictly ahead) is refused a second buy.
	rec = serve(apiTrackTimeline.BuyCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/buy-card", url.Values{}, leader.userId))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected the strict leader's buy to be refused, got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "lead") {
		t.Errorf("expected the refusal to mention the lead, got %q", rec.Body.String())
	}
	if tokens, err := database.GetPlayerTokens(gameId, leader.playerId); err != nil || tokens != tokensAfterFirstBuy {
		t.Errorf("a refused buy must not spend tokens, got %d -> %d (%v)", tokensAfterFirstBuy, tokens, err)
	}

	// A player not in the lead can still buy normally.
	nonLeader := rest[0]
	preTokens, err := database.GetPlayerTokens(gameId, nonLeader.playerId)
	if err != nil {
		t.Fatalf("non-leader tokens: %v", err)
	}
	preLen, err := database.GetPlayerTimeline(gameId, nonLeader.playerId)
	if err != nil {
		t.Fatalf("non-leader timeline: %v", err)
	}
	rec = serve(apiTrackTimeline.BuyCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/buy-card", url.Values{}, nonLeader.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("non-leader buy should succeed: %d %s", rec.Code, rec.Body.String())
	}
	postTokens, err := database.GetPlayerTokens(gameId, nonLeader.playerId)
	if err != nil || postTokens != preTokens-3 {
		t.Errorf("expected the buy to cost 3 tokens, got %d -> %d (%v)", preTokens, postTokens, err)
	}
	postLen, err := database.GetPlayerTimeline(gameId, nonLeader.playerId)
	if err != nil || len(postLen) != len(preLen)+1 {
		t.Errorf("expected the non-leader's timeline to grow by one, got %d -> %d (%v)", len(preLen), len(postLen), err)
	}

	// nonLeader's buy caught them up to a tie with the original leader (2
	// cards each) -- a tie is not "strictly ahead", so the original leader is
	// no longer blocked. Only the still-behind third player (1 card) leaves
	// anyone in sole possession of the lead, and nobody is: this buy must
	// succeed.
	rec = serve(apiTrackTimeline.BuyCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/buy-card", url.Values{}, leader.userId))
	if rec.Code != http.StatusOK {
		t.Errorf("a tie for the lead must not block a buy, got %d %s", rec.Code, rec.Body.String())
	}

	// Now the original leader is alone in front (3 cards vs 2 and 1) --
	// blocked again.
	rec = serve(apiTrackTimeline.BuyCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/buy-card", url.Values{}, leader.userId))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("sole possession of the lead should block a further buy, got %d", rec.Code)
	}
}

// TestSkipResetsReplayUsed guards the softlock where skipping a song left
// ReplayUsed set from the abandoned song, hiding the Restart button (and
// blocking the token spend) for the replacement song even though nobody had
// used a replay on it yet.
func TestSkipResetsReplayUsed(t *testing.T) {
	gameId, lobbyId, _, players, srv := newPlaytestFixesGame(t, "skipreplay", 20, 10, 6)
	defer closePlaytestFixesGame(players, srv)

	current := gamePlayerByUserId(players, mustCurrentPlayerUserId(t, gameId))

	rec := serve(apiTrackTimeline.ReplaySong, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/replay-song", url.Values{}, current.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("replay song: %d %s", rec.Code, rec.Body.String())
	}
	if g, err := database.GetGameById(gameId); err != nil || !g.ReplayUsed {
		t.Fatalf("expected ReplayUsed to be true after a replay, got %v (%v)", g.ReplayUsed, err)
	}

	rec = serve(apiTrackTimeline.SkipCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/skip-card", url.Values{}, current.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("skip card: %d %s", rec.Code, rec.Body.String())
	}

	g, err := database.GetGameById(gameId)
	if err != nil {
		t.Fatalf("get game after skip: %v", err)
	}
	if g.ReplayUsed {
		t.Errorf("expected ReplayUsed to reset to false after a skip, but it stayed true — the replacement song would wrongly hide Restart")
	}

	// The replacement song's own replay must be usable — the fix's whole
	// point is that a token nobody has spent on THIS card should work.
	rec = serve(apiTrackTimeline.ReplaySong, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/replay-song", url.Values{}, current.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("replay on the replacement song should succeed: %d %s", rec.Code, rec.Body.String())
	}
}

// TestGuessTokenTurnPlayerSupersedes guards the corrected guess-token rule:
// among the players who aren't on turn, it's a pure first-to-submit-and-qualify
// race, but the turn player's own qualifying guess always wins the token
// outright — even though (per PlaceCard) it's necessarily submitted after
// every non-turn guess, since the turn player only guesses as part of placing.
func TestGuessTokenTurnPlayerSupersedes(t *testing.T) {
	gameId, lobbyId, _, players, srv := newPlaytestFixesGame(t, "guesssupersede", 20, 10, 6)
	defer closePlaytestFixesGame(players, srv)

	current := gamePlayerByUserId(players, mustCurrentPlayerUserId(t, gameId))
	var others []*player
	for _, p := range players {
		if p != current {
			others = append(others, p)
		}
	}

	card, err := database.GetCurrentCardAnswer(gameId)
	if err != nil || card.CardId == uuid.Nil {
		t.Fatalf("current card: %v", err)
	}

	// A non-turn player guesses fully correctly first -- under a pure race,
	// this would win. It must not, once the turn player also guesses right.
	rec := serve(apiTrackTimeline.SubmitGuess, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/guess",
		url.Values{"guessTitle": {card.Title}, "guessArtist": {card.Artist}}, others[0].userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("non-turn guess: %d %s", rec.Code, rec.Body.String())
	}

	// Zero every other player's tokens so nobody can open a steal window --
	// the round resolves immediately on this placement, isolating the
	// guess-token award from the steal mechanic.
	for _, p := range others {
		if err := database.SetPlayerTokens(gameId, p.playerId, 0); err != nil {
			t.Fatalf("zero tokens: %v", err)
		}
	}
	preTokens, err := database.GetPlayerTokens(gameId, current.playerId)
	if err != nil {
		t.Fatalf("pre-resolve tokens: %v", err)
	}

	// The turn player places (correctly or not doesn't matter for this test)
	// and guesses fully correctly too, bundled into the same submission --
	// necessarily after the non-turn guess above.
	timeline, err := database.GetPlayerTimeline(gameId, current.playerId)
	if err != nil {
		t.Fatalf("current player timeline: %v", err)
	}
	rec = serve(apiTrackTimeline.PlaceCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/place-card",
		url.Values{
			"position":    {fmt.Sprint(correctPosition(timeline, card.ReleaseYear))},
			"guessTitle":  {card.Title},
			"guessArtist": {card.Artist},
		}, current.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("place card: %d %s", rec.Code, rec.Body.String())
	}

	postTokens, err := database.GetPlayerTokens(gameId, current.playerId)
	if err != nil || postTokens != preTokens+1 {
		t.Errorf("expected the turn player to win the guess token despite guessing later, got %d -> %d (%v)", preTokens, postTokens, err)
	}
	if otherTokens, err := database.GetPlayerTokens(gameId, others[0].playerId); err != nil || otherTokens != 0 {
		t.Errorf("expected the earlier non-turn guess to NOT be awarded the token, got %d (%v)", otherTokens, err)
	}
}

func mustCurrentPlayerUserId(t *testing.T, gameId uuid.UUID) uuid.UUID {
	t.Helper()
	g, err := database.GetGameById(gameId)
	if err != nil || !g.CurrentPlayerId.Valid {
		t.Fatalf("no current player: %v", err)
	}
	players, err := database.GetPlayers(gameId)
	if err != nil {
		t.Fatalf("get players: %v", err)
	}
	for _, p := range players {
		if p.PlayerId == g.CurrentPlayerId.UUID {
			return p.UserId
		}
	}
	t.Fatalf("current player not found among players")
	return uuid.Nil
}

func drainAll(players []*player) []string {
	var out []string
	for _, p := range players {
		out = append(out, drain(p)...)
	}
	return out
}

func containsSubstring(lines []string, substr string) bool {
	for _, l := range lines {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}
