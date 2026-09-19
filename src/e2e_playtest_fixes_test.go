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

	apiPages "github.com/gerp93/track-timeline/api/pages"
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

// TestBuyCardCostAndStrictLeaderRestriction guards two related fixes: the
// buy cost (database.BuyCardCost), and the rule that a player strictly ahead
// of every other active player cannot buy at all (ties for the lead still
// can).
func TestBuyCardCostAndStrictLeaderRestriction(t *testing.T) {
	gameId, lobbyId, _, players, srv := newPlaytestFixesGame(t, "buylead", 20, 10, 2*database.BuyCardCost)
	defer closePlaytestFixesGame(players, srv)

	leader, rest := players[0], players[1:]
	// Give the leader one bought card up front (nobody is in the lead yet,
	// so this first buy must succeed) to become the strict leader.
	rec := serve(apiTrackTimeline.BuyCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/buy-card", url.Values{}, leader.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("first buy (nobody in the lead yet) should succeed: %d %s", rec.Code, rec.Body.String())
	}
	tokensAfterFirstBuy, err := database.GetPlayerTokens(gameId, leader.playerId)
	if err != nil || tokensAfterFirstBuy != database.BuyCardCost {
		t.Errorf("expected the buy to cost %d tokens (%d -> %d), got %d (%v)",
			database.BuyCardCost, 2*database.BuyCardCost, database.BuyCardCost, tokensAfterFirstBuy, err)
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
	if err != nil || postTokens != preTokens-database.BuyCardCost {
		t.Errorf("expected the buy to cost %d tokens, got %d -> %d (%v)", database.BuyCardCost, preTokens, postTokens, err)
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

// TestGuessTokenEveryoneWhoQualifiesGetsOne guards the current guess-token
// rule: there is no race for a single token among the players who guess
// correctly (turn player included) — every one of them earns their own
// token the moment their guess is judged, regardless of submit order.
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

	// A non-turn player guesses fully correctly first, and is paid at once --
	// not at reveal.
	rec := serve(apiTrackTimeline.SubmitGuess, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/guess",
		url.Values{"guessTitle": {card.Title}, "guessArtist": {card.Artist}}, others[0].userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("non-turn guess: %d %s", rec.Code, rec.Body.String())
	}
	maxGuess := database.CurrentEconomy().MaxGuessTokens
	if tokens, err := database.GetPlayerTokens(gameId, others[0].playerId); err != nil || tokens != maxGuess {
		t.Fatalf("expected the correct guess to pay %d tokens immediately, got %d (%v)", maxGuess, tokens, err)
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
	// others[0] now holds enough to steal, so a steal window is open and their
	// balance is untouched: the earlier guess was paid once, not again here.
	if otherTokens, err := database.GetPlayerTokens(gameId, others[0].playerId); err != nil || otherTokens != maxGuess {
		t.Errorf("expected the earlier non-turn guess to still hold exactly what it earned (no race, no double pay), got %d (%v)", otherTokens, err)
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
// the moment the guess is judged, so half a guess pays half and a wrong one pays
// nothing.
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

	guess := func(p *player, title, artist string) int {
		t.Helper()
		before, err := database.GetPlayerTokens(gameId, p.playerId)
		if err != nil {
			t.Fatalf("tokens before: %v", err)
		}
		rec := serve(apiTrackTimeline.SubmitGuess, authedRequest(t, "POST",
			"/api/track-timeline/"+lobbyId.String()+"/guess",
			url.Values{"guessTitle": {title}, "guessArtist": {artist}}, p.userId))
		if rec.Code != http.StatusOK {
			t.Fatalf("submit guess: %d %s", rec.Code, rec.Body.String())
		}
		after, err := database.GetPlayerTokens(gameId, p.playerId)
		if err != nil {
			t.Fatalf("tokens after: %v", err)
		}
		return after - before
	}

	per := database.GuessTokensPerPart
	if got := guess(others[0], card.Title, "nobody at all"); got != per {
		t.Errorf("only the title right should pay %d, paid %d", per, got)
	}
	if got := guess(others[1], "zzz qqq", card.Artist); got != per {
		t.Errorf("only the artist right should pay %d, paid %d", per, got)
	}
	if got := guess(current, "zzz qqq", "nobody at all"); got != 0 {
		t.Errorf("a wrong guess should pay nothing, paid %d", got)
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
		"unknown mode": {"playbackMode": {"bogus"}},
		"clip too long": {"clipSeconds": {fmt.Sprint(database.MaxClipSeconds + 1)}},
		"clip too short": {"clipSeconds": {fmt.Sprint(database.MinClipSeconds - 1)}},
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
