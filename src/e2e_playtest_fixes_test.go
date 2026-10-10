package main

import (
	"database/sql"
	"errors"
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

	apiPages "github.com/gerp93/track-timeline/api/pages"
	apiTrackTimeline "github.com/gerp93/track-timeline/api/tracktimeline"
	"github.com/gerp93/track-timeline/database"
)

// Focused regression tests added after a round of playtesting, covering the
// server-testable behaviors from that pass. Client-only fixes (hx-preserve
// on the guess fields, the steal-turn header-badge clear, the button-height
// CSS, and the 20s listen gate) have no server-side counterpart to test here
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
	gameId, err = database.CreateGame(lobbyId, cardsToWin, startingTokens, false, database.PlaybackIntro, 20)
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

// seedTimelineCard puts the next undrawn card from the pile straight onto a
// player's timeline for free, so a test can set up a lead without going through
// Buy (which is itself restricted by the lead rule being tested).
func seedTimelineCard(t *testing.T, gameId, playerId uuid.UUID) {
	t.Helper()
	rows, err := gsDatabase.Query(
		"SELECT CARD_ID, RELEASE_YEAR FROM TRACK_TIMELINE_DRAW_PILE WHERE TRACK_TIMELINE_GAME_ID = ? AND DRAWN = 0 ORDER BY SHUFFLE_ORDER ASC, ID ASC LIMIT 1",
		gameId)
	if err != nil {
		t.Fatalf("seed: read draw pile: %v", err)
	}
	var cardId uuid.UUID
	var year int
	if rows.Next() {
		if err := rows.Scan(&cardId, &year); err != nil {
			rows.Close()
			t.Fatalf("seed: scan: %v", err)
		}
	}
	rows.Close()
	if cardId == uuid.Nil {
		t.Fatal("seed: draw pile is empty")
	}
	if err := gsDatabase.Execute("UPDATE TRACK_TIMELINE_DRAW_PILE SET DRAWN = 1 WHERE TRACK_TIMELINE_GAME_ID = ? AND CARD_ID = ?", gameId, cardId); err != nil {
		t.Fatalf("seed: mark drawn: %v", err)
	}
	if err := gsDatabase.Execute(
		"INSERT INTO TRACK_TIMELINE_PLAYER_TIMELINE (ID, TRACK_TIMELINE_GAME_ID, PLAYER_ID, CARD_ID, RELEASE_YEAR, POSITION) VALUES (UUID(), ?, ?, ?, ?, 0)",
		gameId, playerId, cardId, year); err != nil {
		t.Fatalf("seed: insert: %v", err)
	}
}

// TestBuyCardCostAndLeadRestriction guards two related rules: the buy cost
// (database.BuyCardCost), and that a player in OR TIED for the lead cannot buy
// at all — only someone strictly behind another player can.
func TestBuyCardCostAndLeadRestriction(t *testing.T) {
	gameId, lobbyId, _, players, srv := newPlaytestFixesGame(t, "buylead", 20, 10, 2*database.BuyCardCost)
	defer closePlaytestFixesGame(players, srv)

	buy := func(p *player) *httptest.ResponseRecorder {
		return serve(apiTrackTimeline.BuyCard, authedRequest(t, "POST",
			"/api/track-timeline/"+lobbyId.String()+"/buy-card", url.Values{}, p.userId))
	}

	leader, rest := players[0], players[1:]

	// Everyone level (0 cards each) is a tie for the lead, so nobody may buy.
	if rec := buy(leader); rec.Code != http.StatusBadRequest {
		t.Fatalf("a player tied for the lead with everyone level must not buy, got %d %s", rec.Code, rec.Body.String())
	}

	// Put the leader one card ahead of everyone.
	seedTimelineCard(t, gameId, leader.playerId)
	startTokens, err := database.GetPlayerTokens(gameId, leader.playerId)
	if err != nil {
		t.Fatalf("leader tokens: %v", err)
	}

	// The outright leader is refused.
	rec := buy(leader)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected the leader's buy to be refused, got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "lead") {
		t.Errorf("expected the refusal to mention the lead, got %q", rec.Body.String())
	}
	if tokens, err := database.GetPlayerTokens(gameId, leader.playerId); err != nil || tokens != startTokens {
		t.Errorf("a refused buy must not spend tokens, got %d -> %d (%v)", startTokens, tokens, err)
	}

	// A player who is behind can still buy normally.
	nonLeader := rest[0]
	preTokens, err := database.GetPlayerTokens(gameId, nonLeader.playerId)
	if err != nil {
		t.Fatalf("non-leader tokens: %v", err)
	}
	preLen, err := database.GetPlayerTimeline(gameId, nonLeader.playerId)
	if err != nil {
		t.Fatalf("non-leader timeline: %v", err)
	}
	if rec = buy(nonLeader); rec.Code != http.StatusOK {
		t.Fatalf("a player behind the leader should be able to buy: %d %s", rec.Code, rec.Body.String())
	}
	postTokens, err := database.GetPlayerTokens(gameId, nonLeader.playerId)
	if err != nil || postTokens != preTokens-database.BuyCardCost {
		t.Errorf("expected the buy to cost %d tokens, got %d -> %d (%v)", database.BuyCardCost, preTokens, postTokens, err)
	}
	postLen, err := database.GetPlayerTimeline(gameId, nonLeader.playerId)
	if err != nil || len(postLen) != len(preLen)+1 {
		t.Errorf("expected the buyer's timeline to grow by one, got %d -> %d (%v)", len(preLen), len(postLen), err)
	}

	// That buy leaves nonLeader tied with the original leader (1 card each).
	// Both are now tied for the lead, so neither may buy.
	for name, p := range map[string]*player{"original leader": leader, "player who caught up": nonLeader} {
		if rec = buy(p); rec.Code != http.StatusBadRequest {
			t.Errorf("the %s is tied for the lead and must not buy, got %d %s", name, rec.Code, rec.Body.String())
		}
	}

	// The third player is still behind both and can buy.
	if rec = buy(rest[1]); rec.Code != http.StatusOK {
		t.Errorf("the player still behind should be able to buy: %d %s", rec.Code, rec.Body.String())
	}
}

// TestSkipResetsReplayUsed guards the softlock where skipping a song left
// ReplayUsed set from the abandoned song, hiding the Restart button (and
// blocking the token spend) for the replacement song even though nobody had
// used a replay on it yet.
func TestSkipResetsReplayUsed(t *testing.T) {
	gameId, lobbyId, _, players, srv := newPlaytestFixesGame(t, "skipreplay", 20, 10, 2*database.ReplayCost+database.SkipCost)
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

// TestNewClipBuysAFreshWindowEveryTime guards the paid "different clip" action:
// only the player on turn can buy it, it costs NewClipCost, each buy re-stamps
// a different window on the game (so every later Restart replays the new
// clip), it can be bought repeatedly, and a failed attempt costs nothing.
func TestNewClipBuysAFreshWindowEveryTime(t *testing.T) {
	gameId, lobbyId, _, players, srv := newPlaytestFixesGame(t, "newclip", 20, 10, 3*database.NewClipCost)
	defer closePlaytestFixesGame(players, srv)

	current := gamePlayerByUserId(players, mustCurrentPlayerUserId(t, gameId))
	var other *player
	for _, p := range players {
		if p != current {
			other = p
			break
		}
	}
	buy := func(p *player) *httptest.ResponseRecorder {
		return serve(apiTrackTimeline.NewClip, authedRequest(t, "POST",
			"/api/track-timeline/"+lobbyId.String()+"/new-clip", url.Values{}, p.userId))
	}
	window := func() (int, int) {
		g, err := database.GetGameById(gameId)
		if err != nil {
			t.Fatalf("get game: %v", err)
		}
		return g.ClipStartSeconds, g.ClipEndSeconds
	}

	// Someone not on turn cannot buy, and is not charged.
	if rec := buy(other); rec.Code != http.StatusBadRequest {
		t.Errorf("a player not on turn must not buy a new clip, got %d %s", rec.Code, rec.Body.String())
	}
	if tokens, err := database.GetPlayerTokens(gameId, other.playerId); err != nil || tokens != 3*database.NewClipCost {
		t.Errorf("a refused buy must not spend tokens, got %d (%v)", tokens, err)
	}

	// Buy it three times in a row: each costs the price and moves the window.
	prevStart, _ := window()
	for i := 1; i <= 3; i++ {
		if rec := buy(current); rec.Code != http.StatusOK {
			t.Fatalf("new clip #%d: %d %s", i, rec.Code, rec.Body.String())
		}
		start, end := window()
		if start == prevStart {
			t.Errorf("new clip #%d kept the same start (%d)", i, start)
		}
		if end <= start {
			t.Errorf("new clip #%d has a bad window %d-%d", i, start, end)
		}
		prevStart = start
		want := (3 - i) * database.NewClipCost
		if tokens, err := database.GetPlayerTokens(gameId, current.playerId); err != nil || tokens != want {
			t.Errorf("after buy #%d expected %d tokens, got %d (%v)", i, want, tokens, err)
		}
	}

	// Out of tokens: refused, window untouched.
	beforeStart, _ := window()
	if rec := buy(current); rec.Code != http.StatusBadRequest {
		t.Errorf("a new clip with no tokens must be refused, got %d", rec.Code)
	}
	if afterStart, _ := window(); afterStart != beforeStart {
		t.Errorf("a refused buy must not change the window, got %d -> %d", beforeStart, afterStart)
	}

	// A skip draws a replacement song with its own fresh window and a spendable
	// Restart; New Clip must work on that song too.
	if err := database.SetPlayerTokens(gameId, current.playerId, database.SkipCost+database.NewClipCost); err != nil {
		t.Fatalf("top up: %v", err)
	}
	if rec := serve(apiTrackTimeline.SkipCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/skip-card", url.Values{}, current.userId)); rec.Code != http.StatusOK {
		t.Fatalf("skip: %d %s", rec.Code, rec.Body.String())
	}
	if rec := buy(current); rec.Code != http.StatusOK {
		t.Errorf("new clip on the replacement song should work: %d %s", rec.Code, rec.Body.String())
	}
}

// TestDrawPileBreakdownSplitsNewFromRepeats guards the hover text on the
// "songs left" badge: the remaining pile is split into songs no game has played
// before and songs that already have a draw/deal/buy on record.
func TestDrawPileBreakdownSplitsNewFromRepeats(t *testing.T) {
	gameId, _, _, players, srv := newPlaytestFixesGame(t, "pilesplit", 20, 10, 6)
	defer closePlaytestFixesGame(players, srv)

	before, err := database.GetDrawPileBreakdown(gameId)
	if err != nil {
		t.Fatalf("breakdown: %v", err)
	}
	if before.Total == 0 || before.New+before.Repeat != before.Total {
		t.Fatalf("new + repeat must add up to the total, got %+v", before)
	}

	// Put a song that is still in the pile onto the played-before record, the
	// way another game drawing it would have.
	rows, err := gsDatabase.Query(
		"SELECT CARD_ID FROM TRACK_TIMELINE_DRAW_PILE WHERE TRACK_TIMELINE_GAME_ID = ? AND DRAWN = 0 LIMIT 1", gameId)
	if err != nil {
		t.Fatalf("read pile: %v", err)
	}
	var cardId uuid.UUID
	if rows.Next() {
		if err := rows.Scan(&cardId); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
	}
	rows.Close()
	if cardId == uuid.Nil {
		t.Fatal("draw pile is empty")
	}
	if err := database.LogCardEvent(cardId, database.CardEventDrawn); err != nil {
		t.Fatalf("log event: %v", err)
	}

	after, err := database.GetDrawPileBreakdown(gameId)
	if err != nil {
		t.Fatalf("breakdown after: %v", err)
	}
	if after.Total != before.Total || after.Repeat != before.Repeat+1 || after.New != before.New-1 {
		t.Errorf("one more repeat expected, got %+v -> %+v", before, after)
	}
}

// challengeCall posts to one of the challenge endpoints as a player.
func challengeCall(t *testing.T, handler http.HandlerFunc, lobbyId uuid.UUID, path string, form url.Values, p *player) *httptest.ResponseRecorder {
	t.Helper()
	return serve(handler, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/"+path, form, p.userId))
}

// challengeSetup returns a started three-player game that is between rounds,
// with the player on turn first and the two others after.
func challengeSetup(t *testing.T, prefix string, startingTokens int) (gameId, lobbyId uuid.UUID, players []*player, current *player, others []*player, cleanup func()) {
	t.Helper()
	gameId, lobbyId, _, players, srv := newPlaytestFixesGame(t, prefix, 20, 10, startingTokens)
	current = gamePlayerByUserId(players, mustCurrentPlayerUserId(t, gameId))
	for _, p := range players {
		if p != current {
			others = append(others, p)
		}
	}
	if err := database.SetBetweenRounds(gameId, true); err != nil {
		t.Fatalf("set between rounds: %v", err)
	}
	return gameId, lobbyId, players, current, others, func() { closePlaytestFixesGame(players, srv) }
}

func openChallenge(t *testing.T, lobbyId uuid.UUID, p *player, kind string, tokens int) *httptest.ResponseRecorder {
	t.Helper()
	return challengeCall(t, apiTrackTimeline.OpenChallenge, lobbyId, "challenge", url.Values{
		"kind": {kind}, "tokens": {fmt.Sprint(tokens)}, "reason": {"the AI judge marked my right answer wrong"},
	}, p)
}

func voteChallenge(t *testing.T, lobbyId uuid.UUID, p *player, agree bool) *httptest.ResponseRecorder {
	t.Helper()
	value := "0"
	if agree {
		value = "1"
	}
	return challengeCall(t, apiTrackTimeline.VoteChallenge, lobbyId, "challenge/vote", url.Values{"agree": {value}}, p)
}

// TestChallengeUpheldPaysTokensAndFreezesTheGame walks a whole challenge: it can
// only be raised between rounds, nothing else can happen while it is open, the
// challenger cannot vote on it, and a majority pays it and unfreezes the game.
func TestChallengeUpheldPaysTokensAndFreezesTheGame(t *testing.T) {
	gameId, lobbyId, _, current, others, cleanup := challengeSetup(t, "chalup", 2)
	defer cleanup()
	challenger, voterA, voterB := others[0], others[1], current

	// Not between rounds: refused.
	if err := database.SetBetweenRounds(gameId, false); err != nil {
		t.Fatalf("clear between rounds: %v", err)
	}
	if rec := openChallenge(t, lobbyId, challenger, "tokens", 3); rec.Code != http.StatusBadRequest {
		t.Fatalf("a challenge during a round must be refused, got %d %s", rec.Code, rec.Body.String())
	}
	if err := database.SetBetweenRounds(gameId, true); err != nil {
		t.Fatalf("set between rounds: %v", err)
	}

	// Bad requests: no reason, too many tokens, no kind.
	if rec := challengeCall(t, apiTrackTimeline.OpenChallenge, lobbyId, "challenge",
		url.Values{"kind": {"tokens"}, "tokens": {"3"}, "reason": {" "}}, challenger); rec.Code != http.StatusBadRequest {
		t.Errorf("a challenge with no reason must be refused, got %d", rec.Code)
	}
	if rec := openChallenge(t, lobbyId, challenger, "tokens", database.MaxChallengeTokens+1); rec.Code != http.StatusBadRequest {
		t.Errorf("asking for more than the cap must be refused, got %d", rec.Code)
	}
	if rec := openChallenge(t, lobbyId, challenger, "", 0); rec.Code != http.StatusBadRequest {
		t.Errorf("a challenge that asks for nothing must be refused, got %d", rec.Code)
	}

	before, err := database.GetPlayerTokens(gameId, challenger.playerId)
	if err != nil {
		t.Fatalf("tokens: %v", err)
	}
	if rec := openChallenge(t, lobbyId, challenger, "tokens", 3); rec.Code != http.StatusOK {
		t.Fatalf("open challenge: %d %s", rec.Code, rec.Body.String())
	}

	// While it is open nothing else can happen.
	frozen := map[string]*httptest.ResponseRecorder{
		"play":  challengeCall(t, apiTrackTimeline.PlaySong, lobbyId, "play-song", url.Values{}, current),
		"skip":  challengeCall(t, apiTrackTimeline.SkipCard, lobbyId, "skip-card", url.Values{}, current),
		"buy":   challengeCall(t, apiTrackTimeline.BuyCard, lobbyId, "buy-card", url.Values{}, challenger),
		"guess": challengeCall(t, apiTrackTimeline.SubmitGuess, lobbyId, "guess", url.Values{"guessTitle": {"x"}}, challenger),
		"pause": challengeCall(t, apiTrackTimeline.PauseSong, lobbyId, "pause-song", url.Values{}, current),
	}
	for name, rec := range frozen {
		if rec.Code != http.StatusConflict {
			t.Errorf("%s must be refused while a challenge is open, got %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if g, _ := database.GetGameById(gameId); !g.BetweenRounds {
		t.Error("a refused Play must not end the between-rounds window")
	}

	// One at a time, and the challenger cannot vote on their own.
	if rec := openChallenge(t, lobbyId, voterA, "tokens", 1); rec.Code != http.StatusBadRequest {
		t.Errorf("a second challenge while one is open must be refused, got %d", rec.Code)
	}
	if rec := voteChallenge(t, lobbyId, challenger, true); rec.Code != http.StatusBadRequest {
		t.Errorf("the challenger must not vote on their own challenge, got %d", rec.Code)
	}

	// Two eligible voters: one yes is not a majority, and a vote cannot be repeated.
	if rec := voteChallenge(t, lobbyId, voterA, true); rec.Code != http.StatusOK {
		t.Fatalf("first vote: %d %s", rec.Code, rec.Body.String())
	}
	if rec := voteChallenge(t, lobbyId, voterA, false); rec.Code != http.StatusBadRequest {
		t.Errorf("a second vote from the same player must be refused, got %d", rec.Code)
	}
	if open, _ := database.GetOpenChallenge(gameId); !open.Exists() {
		t.Fatal("one yes out of two voters must not settle it yet")
	}
	if tokens, _ := database.GetPlayerTokens(gameId, challenger.playerId); tokens != before {
		t.Errorf("nothing is paid before the vote is decided, got %d -> %d", before, tokens)
	}

	// The second yes is a majority: paid, and the game is free again.
	if rec := voteChallenge(t, lobbyId, voterB, true); rec.Code != http.StatusOK {
		t.Fatalf("second vote: %d %s", rec.Code, rec.Body.String())
	}
	if open, _ := database.GetOpenChallenge(gameId); open.Exists() {
		t.Fatal("a majority must close the challenge")
	}
	if tokens, _ := database.GetPlayerTokens(gameId, challenger.playerId); tokens != before+3 {
		t.Errorf("an upheld challenge pays what was asked: %d -> %d, want +3", before, tokens)
	}

	// Upheld, so they keep their challenge. And now the next song can start.
	if left, err := database.PlayerHasChallengeLeft(gameId, challenger.playerId); err != nil || !left {
		t.Errorf("a successful challenger keeps their challenge, got %v (%v)", left, err)
	}
	if rec := challengeCall(t, apiTrackTimeline.PlaySong, lobbyId, "play-song", url.Values{}, current); rec.Code != http.StatusOK {
		t.Fatalf("Play must work once the challenge is settled: %d %s", rec.Code, rec.Body.String())
	}
	if g, _ := database.GetGameById(gameId); g.BetweenRounds {
		t.Error("starting the next song must end the between-rounds window")
	}
	if rec := openChallenge(t, lobbyId, challenger, "tokens", 1); rec.Code != http.StatusBadRequest {
		t.Errorf("no challenge once the next song has started, got %d", rec.Code)
	}
}

// TestChallengeRejectedUsesUpTheChallenge guards the baseball rule: a bad
// challenge costs the challenger their challenge for the rest of the game, but
// nobody else's, and nothing is paid.
func TestChallengeRejectedUsesUpTheChallenge(t *testing.T) {
	gameId, lobbyId, _, current, others, cleanup := challengeSetup(t, "chalrej", 2)
	defer cleanup()
	challenger, other := others[0], others[1]

	before, _ := database.GetPlayerTokens(gameId, challenger.playerId)
	if rec := openChallenge(t, lobbyId, challenger, "tokens", 5); rec.Code != http.StatusOK {
		t.Fatalf("open: %d %s", rec.Code, rec.Body.String())
	}
	// Two voters: a single no makes a majority impossible, so it ends at once.
	if rec := voteChallenge(t, lobbyId, other, false); rec.Code != http.StatusOK {
		t.Fatalf("vote: %d %s", rec.Code, rec.Body.String())
	}
	if open, _ := database.GetOpenChallenge(gameId); open.Exists() {
		t.Fatal("a majority against must close the challenge")
	}
	if tokens, _ := database.GetPlayerTokens(gameId, challenger.playerId); tokens != before {
		t.Errorf("a rejected challenge pays nothing, got %d -> %d", before, tokens)
	}

	if left, err := database.PlayerHasChallengeLeft(gameId, challenger.playerId); err != nil || left {
		t.Errorf("a rejected challenge uses up the challenger's one, got left=%v (%v)", left, err)
	}
	if rec := openChallenge(t, lobbyId, challenger, "tokens", 1); rec.Code != http.StatusBadRequest {
		t.Errorf("a player out of challenges must be refused, got %d %s", rec.Code, rec.Body.String())
	}
	if rec := openChallenge(t, lobbyId, current, "tokens", 1); rec.Code != http.StatusOK {
		t.Errorf("someone else still has their challenge, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestChallengeCardClaimAndWithdraw covers the card claim (a free card from the
// pile, placed on the challenger's timeline) and withdrawing, which is free.
func TestChallengeCardClaimAndWithdraw(t *testing.T) {
	gameId, lobbyId, _, current, others, cleanup := challengeSetup(t, "chalcard", 2)
	defer cleanup()
	challenger, other := others[0], others[1]

	// Withdrawing costs nothing: they can challenge again straight away.
	if rec := openChallenge(t, lobbyId, challenger, "card", 0); rec.Code != http.StatusOK {
		t.Fatalf("open: %d %s", rec.Code, rec.Body.String())
	}
	if rec := challengeCall(t, apiTrackTimeline.WithdrawChallenge, lobbyId, "challenge/withdraw", url.Values{}, other); rec.Code != http.StatusBadRequest {
		t.Errorf("only the challenger can withdraw, got %d", rec.Code)
	}
	if rec := challengeCall(t, apiTrackTimeline.WithdrawChallenge, lobbyId, "challenge/withdraw", url.Values{}, challenger); rec.Code != http.StatusOK {
		t.Fatalf("withdraw: %d %s", rec.Code, rec.Body.String())
	}
	if open, _ := database.GetOpenChallenge(gameId); open.Exists() {
		t.Fatal("withdrawing must close the challenge")
	}
	if left, _ := database.PlayerHasChallengeLeft(gameId, challenger.playerId); !left {
		t.Error("withdrawing must not use up the challenge")
	}

	// A card claim that the table agrees to puts one more card on the timeline.
	preTimeline, err := database.GetPlayerTimeline(gameId, challenger.playerId)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if rec := openChallenge(t, lobbyId, challenger, "card", 0); rec.Code != http.StatusOK {
		t.Fatalf("open card claim: %d %s", rec.Code, rec.Body.String())
	}
	for _, voter := range []*player{other, current} {
		if rec := voteChallenge(t, lobbyId, voter, true); rec.Code != http.StatusOK {
			t.Fatalf("vote: %d %s", rec.Code, rec.Body.String())
		}
	}
	postTimeline, err := database.GetPlayerTimeline(gameId, challenger.playerId)
	if err != nil || len(postTimeline) != len(preTimeline)+1 {
		t.Errorf("an upheld card claim adds a card, got %d -> %d (%v)", len(preTimeline), len(postTimeline), err)
	}
}

// TestGuessTokenEveryoneWhoQualifiesGetsOne guards the current guess-token
// rule: there is no race for a single token among the players who guess
// correctly (turn player included) — every one of them earns their own
// tokens when the round's guesses are judged, regardless of submit order.
func TestGuessTokenEveryoneWhoQualifiesGetsOne(t *testing.T) {
	gameId, lobbyId, _, players, srv := newPlaytestFixesGame(t, "guesseveryone", 20, 10, 6)
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

	// Start every other player at zero tokens so the guess below is what
	// gives others[0] their first one.
	for _, p := range others {
		if err := database.SetPlayerTokens(gameId, p.playerId, 0); err != nil {
			t.Fatalf("zero tokens: %v", err)
		}
	}

	// A non-turn player guesses fully correctly first. Nothing is judged as it
	// arrives, so it pays nothing yet.
	rec := serve(apiTrackTimeline.SubmitGuess, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/guess",
		url.Values{"guessTitle": {card.Title}, "guessArtist": {card.Artist}}, others[0].userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("non-turn guess: %d %s", rec.Code, rec.Body.String())
	}
	maxGuess := database.CurrentEconomy().MaxGuessTokens
	if tokens, err := database.GetPlayerTokens(gameId, others[0].playerId); err != nil || tokens != 0 {
		t.Fatalf("a guess must not be judged or paid until the round ends, got %d tokens (%v)", tokens, err)
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
	if err != nil || postTokens != preTokens+maxGuess {
		t.Errorf("expected the turn player to earn %d tokens for their own perfect guess, got %d -> %d (%v)", maxGuess, preTokens, postTokens, err)
	}
	// Nobody held a token, so nobody could steal and the round resolved on the
	// placement: the earlier non-turn guess is paid exactly once, with the turn
	// player's, in the same pass (no race, no double pay).
	if otherTokens, err := database.GetPlayerTokens(gameId, others[0].playerId); err != nil || otherTokens != maxGuess {
		t.Errorf("expected the earlier non-turn guess to be paid %d when the round resolved, got %d (%v)", maxGuess, otherTokens, err)
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

// TestFreshSongsFirstDealsNeverPlayedSongsBeforeTheRest guards the lobby
// setting: with it on, the pile is dealt never-played songs first (random among
// themselves), then everything else at random, and nothing is withheld; with it
// off the pile stays fully random.
func TestFreshSongsFirstDealsNeverPlayedSongsBeforeTheRest(t *testing.T) {
	gameId, _, _, players, srv := newPlaytestFixesGame(t, "freshsongs", 30, 10, 2)
	defer closePlaytestFixesGame(players, srv)

	pileOrder := func() []uuid.UUID {
		rows, err := gsDatabase.Query(
			"SELECT CARD_ID FROM TRACK_TIMELINE_DRAW_PILE WHERE TRACK_TIMELINE_GAME_ID = ? ORDER BY SHUFFLE_ORDER ASC, ID ASC",
			gameId)
		if err != nil {
			t.Fatalf("read pile order: %v", err)
		}
		defer rows.Close()
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				t.Fatalf("scan pile row: %v", err)
			}
			ids = append(ids, id)
		}
		return ids
	}
	seenCards := func() map[uuid.UUID]bool {
		rows, err := gsDatabase.Query(
			"SELECT DISTINCT CARD_ID FROM TRACK_TIMELINE_LOG_CARD WHERE EVENT_TYPE IN ('drawn', 'dealt', 'bought')")
		if err != nil {
			t.Fatalf("read seen cards: %v", err)
		}
		defer rows.Close()
		seen := make(map[uuid.UUID]bool)
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				t.Fatalf("scan seen card: %v", err)
			}
			seen[id] = true
		}
		return seen
	}
	// firstSeenAfterUnseen reports whether a seen card is dealt ahead of an
	// unseen one, i.e. whether the order is NOT "unseen group, then seen group".
	interleaved := func(order []uuid.UUID, seen map[uuid.UUID]bool) bool {
		reachedSeen := false
		for _, id := range order {
			if seen[id] {
				reachedSeen = true
			} else if reachedSeen {
				return true
			}
		}
		return false
	}

	// Mark a dozen more songs as played on top of the ones the started game has
	// already dealt and drawn, so both groups are well populated.
	for _, id := range pileOrder()[:12] {
		if err := database.LogCardEvent(id, database.CardEventDrawn); err != nil {
			t.Fatalf("log drawn: %v", err)
		}
	}
	// The event log is shared by every test in this database, so only count
	// the songs that are in this game's pile.
	seen := seenCards()
	playedInPile := 0
	for _, id := range pileOrder() {
		if seen[id] {
			playedInPile++
		}
	}
	if playedInPile < 12 || playedInPile > 25 {
		t.Fatalf("expected a mix of played and never-played songs, got %d played of 30", playedInPile)
	}

	// On: every reset must deal all never-played songs before any played one,
	// and still contain the whole pile.
	if err := gsDatabase.Execute("UPDATE TRACK_TIMELINE_GAME SET FRESH_SONGS_FIRST = 1 WHERE ID = ?", gameId); err != nil {
		t.Fatalf("enable setting: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := database.ResetGame(gameId); err != nil {
			t.Fatalf("reset: %v", err)
		}
		order := pileOrder()
		if len(order) != 30 {
			t.Fatalf("pile has %d songs after reset, want all 30 (nothing may be withheld)", len(order))
		}
		if interleaved(order, seen) {
			t.Fatalf("reset %d: a played song was dealt ahead of a never-played one", i+1)
		}
	}

	// A game started from that pile deals its starting cards and first song
	// from the never-played group.
	seenBefore := seenCards()
	if err := database.StartGame(gameId); err != nil {
		t.Fatalf("start game: %v", err)
	}
	for _, p := range players {
		timeline, err := database.GetPlayerTimeline(gameId, p.playerId)
		if err != nil || len(timeline) != 1 {
			t.Fatalf("expected one starting card for %s, got %d (%v)", p.name, len(timeline), err)
		}
		if seenBefore[timeline[0].CardId] {
			t.Errorf("%s was dealt a song that had already been played", p.name)
		}
	}
	if current, err := database.GetCurrentCard(gameId); err != nil || seenBefore[current.CardId] {
		t.Errorf("first song in play had already been played (err=%v)", err)
	}

	// Off: the same pile, with the same played songs, stays fully random.
	if err := gsDatabase.Execute("UPDATE TRACK_TIMELINE_GAME SET FRESH_SONGS_FIRST = 0 WHERE ID = ?", gameId); err != nil {
		t.Fatalf("disable setting: %v", err)
	}
	seen = seenCards()
	mixed := false
	for i := 0; i < 5 && !mixed; i++ {
		if err := database.ResetGame(gameId); err != nil {
			t.Fatalf("reset: %v", err)
		}
		mixed = interleaved(pileOrder(), seen)
	}
	if !mixed {
		t.Error("with the setting off, the pile should not be grouped by played/unplayed")
	}
}

// TestCreateLobbyPassesFreshSongsFirstSetting posts the real create-lobby form
// and checks the "never-played songs first" choice lands on the game: on for
// "1", and off for "0" or for a client that never sends the field.
func TestCreateLobbyPassesFreshSongsFirstSetting(t *testing.T) {
	_, _, deckId, players, srv := newPlaytestFixesGame(t, "freshform", 30, 10, 2)
	defer closePlaytestFixesGame(players, srv)

	create := func(name, value string) database.Game {
		form := url.Values{
			"name":       {name},
			"cardsToWin": {"5"},
			"deckId":     {deckId.String()},
		}
		if value != "" {
			form.Set("freshSongsFirst", value)
		}
		rec := serve(apiTrackTimeline.Create, authedRequest(t, "POST",
			"/api/track-timeline/create", form, players[0].userId))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create lobby %q: %d %s", name, rec.Code, rec.Body.String())
		}
		lobbyId, err := uuid.Parse(strings.TrimPrefix(rec.Header().Get("HX-Redirect"), "/track-timeline/"))
		if err != nil {
			t.Fatalf("read lobby id from redirect %q: %v", rec.Header().Get("HX-Redirect"), err)
		}
		game, err := database.GetGame(lobbyId)
		if err != nil || game.Id == uuid.Nil {
			t.Fatalf("read the created game: %v", err)
		}
		return game
	}

	if game := create("freshform on", "1"); !game.FreshSongsFirst {
		t.Error(`freshSongsFirst=1 should turn the setting on`)
	}
	if game := create("freshform off", "0"); game.FreshSongsFirst {
		t.Error(`freshSongsFirst=0 should leave the setting off`)
	}
	if game := create("freshform absent", ""); game.FreshSongsFirst {
		t.Error("a request without the field should leave the setting off")
	}
}

// TestGuessPaysEachPartOnItsOwn guards the token rule: the right title and the
// right artist are worth GuessTokensPerPart each, scored independently and paid
// when the round's guesses are judged, so half a guess pays half and a wrong one
// pays nothing.
func TestGuessPaysEachPartOnItsOwn(t *testing.T) {
	gameId, lobbyId, _, players, srv := newPlaytestFixesGame(t, "guesspart", 20, 10, 0)
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

	tokens := func(p *player) int {
		t.Helper()
		n, err := database.GetPlayerTokens(gameId, p.playerId)
		if err != nil {
			t.Fatalf("tokens: %v", err)
		}
		return n
	}
	guess := func(p *player, title, artist string) {
		t.Helper()
		rec := serve(apiTrackTimeline.SubmitGuess, authedRequest(t, "POST",
			"/api/track-timeline/"+lobbyId.String()+"/guess",
			url.Values{"guessTitle": {title}, "guessArtist": {artist}}, p.userId))
		if rec.Code != http.StatusOK {
			t.Fatalf("submit guess: %d %s", rec.Code, rec.Body.String())
		}
	}

	guess(others[0], card.Title, "nobody at all")
	guess(others[1], "zzz qqq", card.Artist)
	guess(current, "zzz qqq", "nobody at all")
	for _, p := range []*player{others[0], others[1], current} {
		if got := tokens(p); got != 0 {
			t.Fatalf("a guess must not pay before the round ends, %s holds %d", p.name, got)
		}
	}

	if err := database.JudgeRoundGuesses(gameId); err != nil {
		t.Fatalf("judge: %v", err)
	}

	per := database.GuessTokensPerPart
	if got := tokens(others[0]); got != per {
		t.Errorf("only the title right should pay %d, paid %d", per, got)
	}
	if got := tokens(others[1]); got != per {
		t.Errorf("only the artist right should pay %d, paid %d", per, got)
	}
	if got := tokens(current); got != 0 {
		t.Errorf("a wrong guess should pay nothing, paid %d", got)
	}

	// Judging again (the other half of a race to end the round) pays nothing more.
	if err := database.JudgeRoundGuesses(gameId); err != nil {
		t.Fatalf("second judge: %v", err)
	}
	if got := tokens(others[0]); got != per {
		t.Errorf("judging twice must not pay twice, got %d", got)
	}
}

// TestActionsNeedTheirFullPrice guards against a player who is one token short
// getting through: skip, replay and steal eligibility all require the whole
// price, and a refused action must not spend anything.
func TestActionsNeedTheirFullPrice(t *testing.T) {
	gameId, lobbyId, _, players, srv := newPlaytestFixesGame(t, "fullprice", 20, 10, 0)
	defer closePlaytestFixesGame(players, srv)

	current := gamePlayerByUserId(players, mustCurrentPlayerUserId(t, gameId))

	for name, action := range map[string]struct {
		handler http.HandlerFunc
		path    string
		cost    int
	}{
		"skip":   {apiTrackTimeline.SkipCard, "/skip-card", database.SkipCost},
		"replay": {apiTrackTimeline.ReplaySong, "/replay-song", database.ReplayCost},
	} {
		if err := database.SetPlayerTokens(gameId, current.playerId, action.cost-1); err != nil {
			t.Fatalf("set tokens: %v", err)
		}
		rec := serve(action.handler, authedRequest(t, "POST",
			"/api/track-timeline/"+lobbyId.String()+action.path, url.Values{}, current.userId))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s with %d of %d tokens should be refused, got %d", name, action.cost-1, action.cost, rec.Code)
		}
		if tokens, _ := database.GetPlayerTokens(gameId, current.playerId); tokens != action.cost-1 {
			t.Errorf("a refused %s must not spend tokens, balance is %d", name, tokens)
		}

		if err := database.SetPlayerTokens(gameId, current.playerId, action.cost); err != nil {
			t.Fatalf("set tokens: %v", err)
		}
		rec = serve(action.handler, authedRequest(t, "POST",
			"/api/track-timeline/"+lobbyId.String()+action.path, url.Values{}, current.userId))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s with exactly %d tokens should work: %d %s", name, action.cost, rec.Code, rec.Body.String())
		}
		if tokens, _ := database.GetPlayerTokens(gameId, current.playerId); tokens != 0 {
			t.Errorf("%s should spend exactly %d tokens, balance is %d", name, action.cost, tokens)
		}
	}

	// Steal eligibility: everyone but the player on turn, and only with the
	// whole price.
	for _, p := range players {
		if p != current {
			if err := database.SetPlayerTokens(gameId, p.playerId, database.StealCost-1); err != nil {
				t.Fatalf("set tokens: %v", err)
			}
		}
	}
	if eligible, err := database.AnyEligibleToSteal(gameId); err != nil || eligible {
		t.Errorf("nobody has %d tokens, so nobody can steal (eligible=%v err=%v)", database.StealCost, eligible, err)
	}
	other := players[0]
	if other == current {
		other = players[1]
	}
	if err := database.SetPlayerTokens(gameId, other.playerId, database.StealCost); err != nil {
		t.Fatalf("set tokens: %v", err)
	}
	if eligible, err := database.AnyEligibleToSteal(gameId); err != nil || !eligible {
		t.Errorf("a player with exactly %d tokens can steal (eligible=%v err=%v)", database.StealCost, eligible, err)
	}
}

// TestCreateLobbyStartingTokens guards the new starting balance: four by
// default, up to ten, and the removed guess-mode field is simply ignored.
func TestCreateLobbyStartingTokens(t *testing.T) {
	_, _, deckId, players, srv := newPlaytestFixesGame(t, "starttok", 30, 10, 0)
	defer closePlaytestFixesGame(players, srv)

	create := func(name string, extra url.Values) (int, database.Game) {
		form := url.Values{
			"name":       {name},
			"cardsToWin": {"5"},
			"deckId":     {deckId.String()},
		}
		for k, v := range extra {
			form[k] = v
		}
		rec := serve(apiTrackTimeline.Create, authedRequest(t, "POST",
			"/api/track-timeline/create", form, players[0].userId))
		if rec.Code != http.StatusCreated {
			return rec.Code, database.Game{}
		}
		lobbyId, err := uuid.Parse(strings.TrimPrefix(rec.Header().Get("HX-Redirect"), "/track-timeline/"))
		if err != nil {
			t.Fatalf("read lobby id from redirect: %v", err)
		}
		game, err := database.GetGame(lobbyId)
		if err != nil {
			t.Fatalf("read the created game: %v", err)
		}
		return rec.Code, game
	}

	if code, game := create("starttok default", nil); code != http.StatusCreated || game.StartingTokens != database.DefaultStartingTokens {
		t.Errorf("default starting tokens: code %d, got %d, want %d", code, game.StartingTokens, database.DefaultStartingTokens)
	}
	if code, game := create("starttok max", url.Values{"startingTokens": {fmt.Sprint(database.MaxStartingTokens)}}); code != http.StatusCreated || game.StartingTokens != database.MaxStartingTokens {
		t.Errorf("the maximum starting tokens should be allowed: code %d, got %d", code, game.StartingTokens)
	}
	if code, _ := create("starttok too many", url.Values{"startingTokens": {fmt.Sprint(database.MaxStartingTokens + 1)}}); code != http.StatusBadRequest {
		t.Errorf("more than the maximum should be refused, got %d", code)
	}
	if code, _ := create("starttok old client", url.Values{"guessMode": {"title"}}); code != http.StatusCreated {
		t.Errorf("a stale client still sending guessMode should not break lobby creation, got %d", code)
	}
}

// TestAPIStatusCheckIsAdminOnly guards the API Status check: it spends real
// quota and tokens, so a regular player must be refused, and an admin with no
// keys configured is told so without anything being called.
func TestAPIStatusCheckIsAdminOnly(t *testing.T) {
	_, _, _, players, srv := newPlaytestFixesGame(t, "apistatus", 20, 10, 0)
	defer closePlaytestFixesGame(players, srv)

	t.Setenv("TRACK_TIMELINE_YT_API_KEY", "")
	t.Setenv("TRACK_TIMELINE_ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	rec := serve(apiPages.StatusCheck, authedRequest(t, "POST", "/api/status/check", url.Values{}, players[0].userId))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a regular player must be refused, got %d %s", rec.Code, rec.Body.String())
	}

	if err := gsDatabase.SetUserIsAdmin(players[0].userId, true); err != nil {
		t.Fatalf("promote to admin: %v", err)
	}
	rec = serve(apiPages.StatusCheck, authedRequest(t, "POST", "/api/status/check", url.Values{}, players[0].userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("an admin should get the results, got %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"YouTube Data API", "Claude API", "Not configured.", "2 of 2 need attention"} {
		if !strings.Contains(body, want) {
			t.Errorf("status result missing %q: %s", want, body)
		}
	}
}

// TestChangingSettingsMidGame guards the lobby settings that can change once the
// game is running: any player in the lobby may change them, everyone is told
// live (chat plus a "settings:" message), bad values are refused, fields that
// are left out keep their value, and nobody outside the lobby can touch them.
func TestChangingSettingsMidGame(t *testing.T) {
	gameId, lobbyId, _, players, srv := newPlaytestFixesGame(t, "livesettings", 30, 10, 0)
	defer closePlaytestFixesGame(players, srv)

	put := func(p *player, form url.Values) *httptest.ResponseRecorder {
		return serve(apiTrackTimeline.UpdateSettings, authedRequest(t, "PUT",
			"/api/track-timeline/"+lobbyId.String()+"/settings", form, p.userId))
	}
	current := func() database.Game {
		t.Helper()
		g, err := database.GetGameById(gameId)
		if err != nil {
			t.Fatalf("get game: %v", err)
		}
		return g
	}
	undrawn := func() int {
		t.Helper()
		rows, err := gsDatabase.Query("SELECT COUNT(*) FROM TRACK_TIMELINE_DRAW_PILE WHERE TRACK_TIMELINE_GAME_ID = ? AND DRAWN = 0", gameId)
		if err != nil {
			t.Fatalf("count pile: %v", err)
		}
		defer rows.Close()
		var n int
		for rows.Next() {
			_ = rows.Scan(&n)
		}
		return n
	}

	start := current()
	if start.PlaybackMode != database.PlaybackIntro || start.FreshSongsFirst {
		t.Fatalf("unexpected starting settings: %+v", start)
	}

	// Saving what is already set changes nothing and says nothing.
	drainAll(players)
	rec := put(players[1], url.Values{
		"playbackMode": {start.PlaybackMode}, "clipSeconds": {fmt.Sprint(start.ClipSeconds)}, "freshSongsFirst": {"0"},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Nothing changed") {
		t.Errorf("an unchanged save should say so: %d %s", rec.Code, rec.Body.String())
	}
	if chat := chatLines(drainAll(players)); containsSubstring(chat, "changed") {
		t.Errorf("an unchanged save must not announce anything, got %v", chat)
	}

	// A real change, made by a player who is not on turn.
	pileBefore := undrawn()
	rec = put(players[1], url.Values{"playbackMode": {"full"}, "clipSeconds": {"45"}, "freshSongsFirst": {"1"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("change settings: %d %s", rec.Code, rec.Body.String())
	}
	g := current()
	if g.PlaybackMode != database.PlaybackFull || g.ClipSeconds != 45 || !g.FreshSongsFirst {
		t.Errorf("settings were not saved: %+v", g)
	}
	if got := undrawn(); got != pileBefore {
		t.Errorf("reordering the pile must not add or drop cards: %d -> %d", pileBefore, got)
	}
	msgs := drainAll(players)
	if chat := chatLines(msgs); !containsSubstring(chat, "changed playback to “Play the whole song”, clip length to 45 seconds and never-played songs first to on") {
		t.Errorf("chat should say what changed, got %v", chat)
	}
	settingsSeen := 0
	for _, m := range msgs {
		if strings.HasPrefix(m, "settings:") && strings.Contains(m, `"playbackMode":"full"`) &&
			strings.Contains(m, `"clipSeconds":45`) && strings.Contains(m, `"freshSongsFirst":true`) {
			settingsSeen++
		}
	}
	if settingsSeen != len(players) {
		t.Errorf("every player should be sent the new settings, %d of %d were", settingsSeen, len(players))
	}

	// Bad values are refused and change nothing.
	for name, form := range map[string]url.Values{
		"unknown mode":      {"playbackMode": {"bogus"}},
		"clip too long":     {"clipSeconds": {fmt.Sprint(database.MaxClipSeconds + 1)}},
		"clip too short":    {"clipSeconds": {fmt.Sprint(database.MinClipSeconds - 1)}},
		"clip not a number": {"clipSeconds": {"abc"}},
	} {
		if rec := put(players[0], form); rec.Code != http.StatusBadRequest {
			t.Errorf("%s should be refused, got %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if after := current(); after.PlaybackMode != g.PlaybackMode || after.ClipSeconds != g.ClipSeconds || after.FreshSongsFirst != g.FreshSongsFirst {
		t.Errorf("refused changes must not alter anything: %+v -> %+v", g, after)
	}

	// A field that is left out keeps its value.
	if rec := put(players[2], url.Values{"clipSeconds": {"20"}}); rec.Code != http.StatusOK {
		t.Fatalf("partial change: %d %s", rec.Code, rec.Body.String())
	}
	if after := current(); after.ClipSeconds != 20 || after.PlaybackMode != database.PlaybackFull || !after.FreshSongsFirst {
		t.Errorf("only the clip length should have changed: %+v", after)
	}

	// Turning the toggle back off is a change like any other.
	if rec := put(players[0], url.Values{"freshSongsFirst": {"0"}}); rec.Code != http.StatusOK {
		t.Fatalf("turn the toggle off: %d %s", rec.Code, rec.Body.String())
	}
	if current().FreshSongsFirst {
		t.Error("the never-played toggle should be off again")
	}

	// Someone who is not in the lobby cannot change anything.
	if err := gsDatabase.CreateUser("livesettings_outsider", "unused-not-a-login", true); err != nil {
		t.Fatalf("create outsider: %v", err)
	}
	outsiderId, err := gsDatabase.GetUserIdByName("livesettings_outsider")
	if err != nil {
		t.Fatalf("get outsider: %v", err)
	}
	if rec := put(&player{userId: outsiderId}, url.Values{"clipSeconds": {"10"}}); rec.Code != http.StatusUnauthorized {
		t.Errorf("someone outside the lobby must be refused, got %d %s", rec.Code, rec.Body.String())
	}
	if current().ClipSeconds != 20 {
		t.Error("an outsider's refused request changed the clip length")
	}
}

// TestSkippedSongGuessesAreJudgedWithTheRound guards the rule that a skip does
// not throw guesses away: a round can span several songs, a guess stays against
// the song it was typed for, and everything is judged and paid together when the
// round ends -- including a draft the player never pressed Guess on, and a guess
// that reached the server a beat after the skip.
func TestSkippedSongGuessesAreJudgedWithTheRound(t *testing.T) {
	gameId, lobbyId, _, players, srv := newPlaytestFixesGame(t, "skipguess", 20, 10, 10)
	defer closePlaytestFixesGame(players, srv)

	current := gamePlayerByUserId(players, mustCurrentPlayerUserId(t, gameId))
	var others []*player
	for _, p := range players {
		if p != current {
			others = append(others, p)
		}
	}
	skipped, err := database.GetCurrentCardAnswer(gameId)
	if err != nil || skipped.CardId == uuid.Nil {
		t.Fatalf("current card: %v", err)
	}
	post := func(h http.HandlerFunc, path string, p *player, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		return serve(h, authedRequest(t, "POST", "/api/track-timeline/"+lobbyId.String()+"/"+path, form, p.userId))
	}
	tokens := func(p *player) int {
		t.Helper()
		n, err := database.GetPlayerTokens(gameId, p.playerId)
		if err != nil {
			t.Fatalf("tokens: %v", err)
		}
		return n
	}

	// others[0] locks in a perfect guess; others[1] has only typed the title and
	// never pressed Guess -- the browser saved it as a draft.
	rec := post(apiTrackTimeline.SubmitGuess, "guess", others[0], url.Values{
		"cardId": {skipped.CardId.String()}, "guessTitle": {skipped.Title}, "guessArtist": {skipped.Artist},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("lock guess: %d %s", rec.Code, rec.Body.String())
	}
	rec = post(apiTrackTimeline.SaveGuessDraft, "guess-draft", others[1], url.Values{
		"cardId": {skipped.CardId.String()}, "guessTitle": {skipped.Title},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("save draft: %d %s", rec.Code, rec.Body.String())
	}

	// The player on turn skips.
	rec = post(apiTrackTimeline.SkipCard, "skip-card", current, url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("skip: %d %s", rec.Code, rec.Body.String())
	}
	next, err := database.GetCurrentCardAnswer(gameId)
	if err != nil || next.CardId == uuid.Nil || next.CardId == skipped.CardId {
		t.Fatalf("expected a different song after the skip: %v", err)
	}
	if tokens(others[0]) != 10 || tokens(others[1]) != 10 {
		t.Errorf("a skip must not judge or pay anything yet, got %d and %d", tokens(others[0]), tokens(others[1]))
	}

	// others[1] presses Enter a moment after the skip, on boxes they typed for
	// the old song: the form still carries the old song's id, so it is filed
	// there, not against the new one -- and can be told so.
	rec = post(apiTrackTimeline.SubmitGuess, "guess", others[1], url.Values{
		"cardId": {skipped.CardId.String()}, "guessTitle": {skipped.Title}, "guessArtist": {skipped.Artist},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "skipped") {
		t.Fatalf("a guess for the skipped song should still count and say so, got %d %q", rec.Code, rec.Body.String())
	}
	if guessed, err := database.HasGuessed(gameId, others[1].playerId); err != nil || guessed {
		t.Errorf("a guess on the skipped song must not use up the guess on the new one (%v, %v)", guessed, err)
	}

	// A made-up song is refused, and nothing is stored for it.
	rec = post(apiTrackTimeline.SubmitGuess, "guess", others[1], url.Values{
		"cardId": {uuid.NewString()}, "guessTitle": {"anything"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a guess for a song that is not part of this round should be refused, got %d", rec.Code)
	}

	// The new song gets its own guess, here a wrong one.
	rec = post(apiTrackTimeline.SubmitGuess, "guess", others[0], url.Values{
		"cardId": {next.CardId.String()}, "guessTitle": {"zzz qqq"}, "guessArtist": {"nobody at all"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("guess on the new song: %d %s", rec.Code, rec.Body.String())
	}

	// End the round: nobody holds a token, so a correct placement resolves it.
	for _, p := range players {
		if err := database.SetPlayerTokens(gameId, p.playerId, 0); err != nil {
			t.Fatalf("zero tokens: %v", err)
		}
	}
	timeline, err := database.GetPlayerTimeline(gameId, current.playerId)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	rec = post(apiTrackTimeline.PlaceCard, "place-card", current, url.Values{
		"position": {fmt.Sprint(correctPosition(timeline, next.ReleaseYear))},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("place: %d %s", rec.Code, rec.Body.String())
	}

	per := database.GuessTokensPerPart
	if got := tokens(others[0]); got != 2*per {
		t.Errorf("a perfect guess on the skipped song should pay %d when the round ends, paid %d", 2*per, got)
	}
	if got := tokens(others[1]); got != 2*per {
		t.Errorf("the guess that landed after the skip should pay %d, paid %d", 2*per, got)
	}
	if got := tokens(current); got != 0 {
		t.Errorf("nothing was guessed by the player on turn, got %d", got)
	}

	// The round is over: its songs are no longer part of any round.
	if inRound, err := database.IsRoundCard(gameId, skipped.CardId); err != nil || inRound {
		t.Errorf("the skipped song should leave the round once it ends (%v, %v)", inRound, err)
	}
	if guesses, err := database.GetGuesses(gameId); err != nil || len(guesses) != 0 {
		t.Errorf("the round's guesses should be cleared once it ends, got %d (%v)", len(guesses), err)
	}
	rec = post(apiTrackTimeline.SubmitGuess, "guess", others[0], url.Values{
		"cardId": {skipped.CardId.String()}, "guessTitle": {skipped.Title},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a guess for a song from a finished round should be refused, got %d", rec.Code)
	}
}

// A song whose video would not play never really played, so guesses made on it
// are dropped rather than judged; an ordinary skip keeps them.
func TestDeadVideoDropsGuessesButASkipKeepsThem(t *testing.T) {
	gameId, _, _, players, srv := newPlaytestFixesGame(t, "deadguess", 20, 10, 10)
	defer closePlaytestFixesGame(players, srv)

	voter := players[0]
	first, err := database.GetCurrentCard(gameId)
	if err != nil {
		t.Fatalf("current card: %v", err)
	}

	if err := database.SaveGuess(gameId, voter.playerId, first.CardId, "a title", "", false); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	if err := database.SkipCurrentCard(gameId, false); err != nil {
		t.Fatalf("dead-video skip: %v", err)
	}
	if guesses, err := database.GetGuesses(gameId); err != nil || len(guesses) != 0 {
		t.Errorf("guesses on a dead video should be dropped, got %d (%v)", len(guesses), err)
	}
	if inRound, _ := database.IsRoundCard(gameId, first.CardId); inRound {
		t.Error("a dead video must not stay part of the round")
	}

	second, err := database.GetCurrentCard(gameId)
	if err != nil {
		t.Fatalf("current card: %v", err)
	}
	if err := database.SaveGuess(gameId, voter.playerId, second.CardId, "a title", "an artist", false); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	if err := database.SkipCurrentCard(gameId, true); err != nil {
		t.Fatalf("skip: %v", err)
	}
	guesses, err := database.GetGuesses(gameId)
	if err != nil || len(guesses) != 1 {
		t.Fatalf("an ordinary skip should keep the guess, got %d (%v)", len(guesses), err)
	}
	if guesses[0].CardId != second.CardId || guesses[0].IsCurrentSong || guesses[0].Locked {
		t.Errorf("the kept guess should be an unlocked draft against the skipped song: %+v", guesses[0])
	}
	if inRound, _ := database.IsRoundCard(gameId, second.CardId); !inRound {
		t.Error("a skipped song should stay part of the round so its guesses are still accepted")
	}
}

// A draft is replaced as the player keeps typing, an empty one removes it, and
// once locked a late draft can no longer rewrite it.
func TestGuessDraftsReplaceUntilLocked(t *testing.T) {
	gameId, _, _, players, srv := newPlaytestFixesGame(t, "draftlock", 20, 10, 10)
	defer closePlaytestFixesGame(players, srv)

	p := players[0]
	card, err := database.GetCurrentCard(gameId)
	if err != nil {
		t.Fatalf("current card: %v", err)
	}
	read := func() (database.Guess, bool) {
		t.Helper()
		g, ok, err := database.GetPlayerGuess(gameId, p.playerId)
		if err != nil {
			t.Fatalf("read guess: %v", err)
		}
		return g, ok
	}

	for _, typed := range []string{"z", "zom", "zombie"} {
		if err := database.SaveGuess(gameId, p.playerId, card.CardId, typed, "", false); err != nil {
			t.Fatalf("draft %q: %v", typed, err)
		}
	}
	if g, ok := read(); !ok || g.TitleGuess != "zombie" || g.Locked {
		t.Fatalf("the latest draft should win and stay unlocked: %+v (%v)", g, ok)
	}

	if err := database.SaveGuess(gameId, p.playerId, card.CardId, "", "", false); err != nil {
		t.Fatalf("empty draft: %v", err)
	}
	if _, ok := read(); ok {
		t.Fatal("clearing the boxes should remove the draft")
	}

	if err := database.SaveGuess(gameId, p.playerId, card.CardId, "zombie", "cranberries", true); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if err := database.SaveGuess(gameId, p.playerId, card.CardId, "something else", "", false); err != nil {
		t.Fatalf("late draft: %v", err)
	}
	if g, ok := read(); !ok || g.TitleGuess != "zombie" || g.ArtistGuess != "cranberries" || !g.Locked {
		t.Errorf("a late draft must not rewrite a locked guess: %+v (%v)", g, ok)
	}
	if err := database.SaveGuess(gameId, p.playerId, card.CardId, "again", "", true); !errors.Is(err, database.ErrGuessLocked) {
		t.Errorf("locking twice should be refused with ErrGuessLocked, got %v", err)
	}
}
