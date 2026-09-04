package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	gsApi "github.com/gerp93/gameshell-framework/api"
	gsAuth "github.com/gerp93/gameshell-framework/auth"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	gsStatic "github.com/gerp93/gameshell-framework/static"
	gsWebsocket "github.com/gerp93/gameshell-framework/websocket"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	apiTrackTimeline "github.com/gerp93/track-timeline/api/tracktimeline"
	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/static"
)

// End-to-end exercise of the game against a real database, driving the real
// HTTP handlers and real websocket clients. Sessions are minted with
// gsAuth.SetUserId rather than by logging in; the signing secret is
// per-process, so an in-process cookie is valid here. Mirrors
// timeline-trivia's e2e_test.go.

type player struct {
	name     string
	userId   uuid.UUID
	playerId uuid.UUID
	conn     *websocket.Conn
	received chan string
}

func setupSchema(t *testing.T) {
	t.Helper()
	for _, f := range gsStatic.SQLFiles {
		if err := gsDatabase.RunFile(f); err != nil {
			t.Fatalf("framework schema %s: %v", f, err)
		}
	}
	// This game uses decks, so its schema needs the framework's deck tables
	// too — mirrors main.go's gsBootstrap.ApplyFeatureSchema(features) call.
	// Not calling ApplyFeatureSchema itself: it log.Fatalln's on error, which
	// would abort the whole test binary instead of just failing this test.
	for _, f := range gsStatic.DeckSQLFiles {
		if err := gsDatabase.RunFile(f); err != nil {
			t.Fatalf("framework deck schema %s: %v", f, err)
		}
	}
	for _, f := range static.SQLFiles {
		if err := runGameFile(f); err != nil {
			t.Fatalf("game schema %s: %v", f, err)
		}
	}
}

func runGameFile(path string) error {
	b, err := static.StaticFiles.ReadFile(path)
	if err != nil {
		return err
	}
	return gsDatabase.Execute(string(b))
}

func authedRequest(t *testing.T, method, target string, form url.Values, userId uuid.UUID) *http.Request {
	t.Helper()
	var r *http.Request
	if form != nil {
		r = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	rec := httptest.NewRecorder()
	gsAuth.SetUserId(rec, userId)
	for _, c := range rec.Result().Cookies() {
		r.AddCookie(c)
	}
	// Handlers read {lobbyId} via PathValue, which only a ServeMux populates —
	// set it from the path directly since these bypass routing.
	if parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/"); len(parts) >= 3 {
		if parts[0] == "api" && parts[1] == "track-timeline" {
			r.SetPathValue("lobbyId", parts[2])
		}
	}
	return r
}

func serve(h http.HandlerFunc, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	gsApi.MiddlewareForAPIs(h).ServeHTTP(rec, r)
	return rec
}

// drain collects everything a client received within a short settle window.
func drain(p *player) []string {
	var out []string
	deadline := time.After(700 * time.Millisecond)
	for {
		select {
		case m := <-p.received:
			out = append(out, m)
		case <-deadline:
			return out
		}
	}
}

func resultPayloads(msgs []string) []map[string]any {
	var out []map[string]any
	for _, m := range msgs {
		if !strings.HasPrefix(m, "result:") {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(m[len("result:"):]), &p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func chatLines(msgs []string) []string {
	var out []string
	for _, m := range msgs {
		if strings.HasPrefix(m, "result:") || strings.HasPrefix(m, "status:") ||
			strings.HasPrefix(m, "song:") || strings.HasPrefix(m, "alert:") ||
			strings.HasPrefix(m, "lobbyMessage:") || m == "refresh" || m == "reload" || m == "songStop" {
			continue
		}
		out = append(out, m)
	}
	return out
}

func statusLines(msgs []string) []string {
	var out []string
	for _, m := range msgs {
		if strings.HasPrefix(m, "status:") {
			out = append(out, strings.TrimPrefix(m, "status:"))
		}
	}
	return out
}

func alertLines(msgs []string) []string {
	var out []string
	for _, m := range msgs {
		if strings.HasPrefix(m, "alert:") {
			out = append(out, strings.TrimPrefix(m, "alert:"))
		}
	}
	return out
}

// correctPosition returns the sorted insertion point for releaseYear. Since
// every test card has a distinct year, this is the single correct answer.
func correctPosition(timeline []database.TimelineCard, releaseYear int) int {
	for i, c := range timeline {
		if releaseYear < c.ReleaseYear {
			return i
		}
	}
	return len(timeline)
}

// wrongPosition picks any position other than the correct one. A timeline
// always has at least one dealt card (len >= 1), so there are always at
// least two candidate positions (0 and len(timeline)) to choose from.
func wrongPosition(timeline []database.TimelineCard, releaseYear int) int {
	correct := correctPosition(timeline, releaseYear)
	if correct != 0 {
		return 0
	}
	return len(timeline)
}

func TestTrackTimelineEndToEnd(t *testing.T) {
	// This test seeds and mutates freely, so it refuses to touch anything but
	// a purpose-made throwaway database.
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

	// ---- seed users, deck, cards -------------------------------------------
	names := []string{"e2e_alice", "e2e_bob", "e2e_carol"}
	players := make([]*player, 0, len(names))
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
	// Alice gets a win celebration so the popup payload can be checked.
	if err := gsDatabase.SetUserWinMessage(players[0].userId, "GET REKT"); err != nil {
		t.Fatalf("set win message: %v", err)
	}
	if err := gsDatabase.SetUserWinGif(players[0].userId, []byte("GIF89a-fake-bytes"), "image/gif"); err != nil {
		t.Fatalf("set win gif: %v", err)
	}
	if err := gsDatabase.SetUserWinVideo(players[0].userId, "dQw4w9wgXcQ", 12); err != nil {
		t.Fatalf("set win video: %v", err)
	}
	// Every player gets the same lose celebration so discarded-round assertions
	// work regardless of who the shuffle puts on turn.
	for _, p := range players {
		if err := gsDatabase.SetUserLoseMessage(p.userId, "OOF"); err != nil {
			t.Fatalf("set lose message: %v", err)
		}
		if err := gsDatabase.SetUserLoseGif(p.userId, []byte("GIF89a-fake-lose-bytes"), "image/gif"); err != nil {
			t.Fatalf("set lose gif: %v", err)
		}
	}

	deckId, err := gsDatabase.CreateDeck("e2e deck", "", true)
	if err != nil {
		t.Fatalf("create deck: %v", err)
	}
	for i := 0; i < 60; i++ {
		year := sql.NullInt64{Int64: int64(1000 + i*10), Valid: true}
		// The deck+video-id pair is unique, so each card needs its own
		// 11-character id even though none of them is ever really played.
		videoId := fmt.Sprintf("e2eVideo%03d", i)
		_, err := database.CreateCard(deckId, videoId,
			fmt.Sprintf("Song %d", i), fmt.Sprintf("Artist %d", i), year, uuid.NullUUID{})
		if err != nil {
			t.Fatalf("create card %d: %v", i, err)
		}
	}

	// ---- lobby, players, game ----------------------------------------------
	lobbyId, err := database.CreateLobby("e2e lobby", "", "")
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
	gameId, err := database.CreateGame(lobbyId, 5, 2, database.GuessModeBoth, database.DefaultGuessMatchPercent, database.GuessJudgeLocal, database.PlaybackIntro, 20)
	if err != nil {
		t.Fatalf("create game: %v", err)
	}
	if err := database.InitializeDrawPile(gameId, []uuid.UUID{deckId}, nil); err != nil {
		t.Fatalf("init draw pile: %v", err)
	}
	if err := gsDatabase.SetLobbyTurnTimerSeconds(lobbyId, 30); err != nil {
		t.Fatalf("set turn timer: %v", err)
	}

	// ---- real websocket clients, one per player ----------------------------
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/lobby/{lobbyId}", gsWebsocket.ServeWs)
	srv := httptest.NewServer(mux)
	defer srv.Close()

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
	defer func() {
		for _, p := range players {
			_ = p.conn.Close()
		}
	}()
	time.Sleep(400 * time.Millisecond)
	for _, p := range players {
		drain(p) // discard join noise
	}

	byPlayerId := map[uuid.UUID]*player{}
	for _, p := range players {
		byPlayerId[p.playerId] = p
	}
	currentPlayer := func() *player {
		g, err := database.GetGameById(gameId)
		if err != nil || !g.CurrentPlayerId.Valid {
			t.Fatalf("no current player: %v", err)
		}
		return byPlayerId[g.CurrentPlayerId.UUID]
	}
	otherPlayers := func(exclude ...*player) []*player {
		excluded := map[uuid.UUID]bool{}
		for _, p := range exclude {
			excluded[p.playerId] = true
		}
		var out []*player
		for _, p := range players {
			if !excluded[p.playerId] {
				out = append(out, p)
			}
		}
		return out
	}
	turnOrder := func() []string {
		ps, err := database.GetPlayers(gameId)
		if err != nil {
			t.Fatalf("get players: %v", err)
		}
		var out []string
		for _, p := range ps {
			if p.IsActive {
				out = append(out, p.UserName)
			}
		}
		return out
	}

	// exhaustStealWithMiss claims the sole steal attempt (if a window is open
	// — it opens on every placement now, right or wrong, see steal.go) with
	// one of the other eligible players and deliberately misses it, so the
	// round falls back to judging the turn player's own original placement —
	// without ever sleeping on a real timer. A no-op if no steal window
	// opened (nobody was eligible to steal). cardYear is the answer the wrong
	// position is computed against.
	exhaustStealWithMiss := func(turnPlayer *player, cardYear int) {
		if g, _ := database.GetGameById(gameId); g.RoundPhase != database.PhaseStealJoin {
			return
		}
		for _, other := range otherPlayers(turnPlayer) {
			rec := serve(apiTrackTimeline.ClaimSteal, authedRequest(t, "POST",
				"/api/track-timeline/"+lobbyId.String()+"/claim-steal", url.Values{}, other.userId))
			if rec.Code != http.StatusOK {
				continue
			}
			stealerTimeline, _ := database.GetPlayerTimeline(gameId, other.playerId)
			serve(apiTrackTimeline.AttemptSteal, authedRequest(t, "POST",
				"/api/track-timeline/"+lobbyId.String()+"/attempt-steal",
				url.Values{"position": {fmt.Sprint(wrongPosition(stealerTimeline, cardYear))}}, other.userId))
			break
		}
		for _, p := range players {
			drain(p)
		}
	}

	// ================= 1. start game, shuffled + stable order ===============
	rec := serve(apiTrackTimeline.StartGame,
		authedRequest(t, "POST", "/api/track-timeline/"+lobbyId.String()+"/start", url.Values{}, players[0].userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("start game: %d %s", rec.Code, rec.Body.String())
	}
	order1 := turnOrder()
	if len(order1) != 3 {
		t.Fatalf("expected 3 players in order, got %v", order1)
	}
	// The very first shuffle's "previous" baseline is JOIN_ORDER (no
	// TRACK_TIMELINE_PLAYER_ORDER rows exist yet), and ShufflePlayerOrder
	// guarantees the new order differs from that baseline — so the first game
	// is provably shuffled, not merely "probably".
	joinOrder := []string{players[0].name, players[1].name, players[2].name}
	if strings.Join(order1, ",") == strings.Join(joinOrder, ",") {
		t.Errorf("first game's order equals plain join order %v — shuffle guarantee not holding", joinOrder)
	}

	foundOrderChat := false
	for _, p := range players {
		for _, l := range chatLines(drain(p)) {
			if strings.Contains(l, "Game started") && strings.Contains(l, "turn order") {
				foundOrderChat = true
			}
		}
	}
	if !foundOrderChat {
		t.Errorf("expected a 'Game started ... turn order' chat line")
	}

	// Every player should hold the starting token count and one dealt card.
	for _, p := range players {
		tl, err := database.GetPlayerTimeline(gameId, p.playerId)
		if err != nil || len(tl) != 1 {
			t.Fatalf("%s: expected 1 dealt card, got %d (%v)", p.name, len(tl), err)
		}
		tokens, err := database.GetPlayerTokens(gameId, p.playerId)
		if err != nil || tokens != 2 {
			t.Errorf("%s: expected 2 starting tokens, got %d (%v)", p.name, tokens, err)
		}
	}

	// ================= 2. wrong placement opens a steal-join window ==========
	guesser := currentPlayer()
	card, err := database.GetCurrentCardAnswer(gameId) // test-only: reading the answer server-side to script the scenario
	if err != nil {
		t.Fatalf("current card: %v", err)
	}
	guesserTimeline, err := database.GetPlayerTimeline(gameId, guesser.playerId)
	if err != nil || len(guesserTimeline) != 1 {
		t.Fatalf("guesser timeline: %d (%v)", len(guesserTimeline), err)
	}
	wrongPos := wrongPosition(guesserTimeline, card.ReleaseYear)

	rec = serve(apiTrackTimeline.PlaceCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/place-card",
		url.Values{"position": {fmt.Sprint(wrongPos)}}, guesser.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("place card (wrong): %d %s", rec.Code, rec.Body.String())
	}

	// Both other players still hold tokens, so the round must not resolve yet
	// — it should be waiting in the steal-join phase.
	g, err := database.GetGameById(gameId)
	if err != nil {
		t.Fatalf("get game: %v", err)
	}
	if g.RoundPhase != database.PhaseStealJoin {
		t.Fatalf("expected steal-join phase after a wrong placement with tokens outstanding, got %q", g.RoundPhase)
	}
	sawStealAnnounce := false
	for _, p := range players {
		msgs := drain(p)
		for _, l := range statusLines(msgs) {
			if strings.Contains(l, "steal window") {
				sawStealAnnounce = true
			}
		}
		for _, l := range chatLines(msgs) {
			if strings.Contains(l, "steal window") {
				sawStealAnnounce = true
			}
		}
	}
	if !sawStealAnnounce {
		t.Errorf("expected a status line announcing the steal window")
	}

	// ================= 3. claiming your own steal window is refused =========
	rec = serve(apiTrackTimeline.ClaimSteal, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/claim-steal", url.Values{}, guesser.userId))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("guesser claiming the steal on their own placement should be rejected, got %d", rec.Code)
	}

	// ================= 4. a claimed, correct steal takes the card ===========
	// There is exactly one steal attempt per round now — first claim wins the
	// race, and a second claim after that is simply refused (no queue).
	stealers := otherPlayers(guesser)
	stealer := stealers[0]
	bystander := stealers[1]

	stealerTimeline, err := database.GetPlayerTimeline(gameId, stealer.playerId)
	if err != nil || len(stealerTimeline) != 1 {
		t.Fatalf("stealer timeline: %d (%v)", len(stealerTimeline), err)
	}
	rightPos := correctPosition(stealerTimeline, card.ReleaseYear)

	preClaimTokens, err := database.GetPlayerTokens(gameId, stealer.playerId)
	if err != nil || preClaimTokens != 2 {
		t.Fatalf("stealer pre-claim tokens: %d (%v)", preClaimTokens, err)
	}
	rec = serve(apiTrackTimeline.ClaimSteal, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/claim-steal", url.Values{}, stealer.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("claim steal: %d %s", rec.Code, rec.Body.String())
	}
	// A claim spends the token immediately — there is no separate "join for
	// free, pay when your turn begins" step now that there is only one
	// attempt, not a queue.
	postClaimTokens, err := database.GetPlayerTokens(gameId, stealer.playerId)
	if err != nil || postClaimTokens != 1 {
		t.Errorf("claiming should spend the stealer's token immediately (2 -> 1), got %d (%v)", postClaimTokens, err)
	}

	// The bystander tries to claim after the stealer already did — refused,
	// since the sole claim is already taken.
	rec = serve(apiTrackTimeline.ClaimSteal, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/claim-steal", url.Values{}, bystander.userId))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("claiming after someone else already claimed should be rejected, got %d", rec.Code)
	}
	for _, p := range players {
		drain(p)
	}

	if g, _ := database.GetGameById(gameId); g.RoundPhase != database.PhaseStealTurn {
		t.Fatalf("expected steal_turn once the sole claim succeeded, got %q", g.RoundPhase)
	}
	if g, _ := database.GetGameById(gameId); !g.StealerPlayerId.Valid || g.StealerPlayerId.UUID != stealer.playerId {
		t.Fatalf("expected %s to be the stealer, got %+v", stealer.name, g.StealerPlayerId)
	}

	rec = serve(apiTrackTimeline.AttemptSteal, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/attempt-steal",
		url.Values{"position": {fmt.Sprint(rightPos)}}, stealer.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("attempt steal (correct): %d %s", rec.Code, rec.Body.String())
	}

	sawSteal := false
	for _, p := range players {
		for _, pl := range resultPayloads(drain(p)) {
			if pl["type"] == "won" && pl["wonByChallenge"] == true {
				sawSteal = true
				if bm, _ := pl["bottomMessage"].(string); !strings.Contains(bm, "stole") {
					t.Errorf("%s: steal result missing 'stole' in bottomMessage: %q", p.name, bm)
				}
				if next, _ := pl["nextPlayerName"].(string); next == "" {
					t.Errorf("%s: steal result had no nextPlayerName", p.name)
				}
				// Steal winner's win celebration rides on the shared popup
				// when they configured one — only Alice has one in this test.
				if stealer.userId == players[0].userId {
					if pl["celebration"] != "GET REKT" || pl["hasGif"] != true || pl["userId"] != stealer.userId.String() {
						t.Errorf("%s: steal result missing Alice's win celebration: %v", p.name, pl)
					}
				} else if pl["userId"] != stealer.userId.String() {
					t.Errorf("%s: steal result userId should be the stealer, got %v", p.name, pl["userId"])
				}
			}
		}
	}
	if !sawSteal {
		t.Errorf("expected a won/wonByChallenge result after the steal attempt")
	}

	postSteal, err := database.GetPlayerTimeline(gameId, stealer.playerId)
	if err != nil || len(postSteal) != 2 {
		t.Errorf("stealer should have won the card (timeline 1 -> 2), got %d (%v)", len(postSteal), err)
	}
	guesserAfter, err := database.GetPlayerTimeline(gameId, guesser.playerId)
	if err != nil || len(guesserAfter) != 1 {
		t.Errorf("original guesser's timeline must not grow on a lost steal, got %d (%v)", len(guesserAfter), err)
	}

	// ================= 4b. an unclaimed steal-join window resolves itself,
	// falling back to the (wrong) original placement — discarded ============
	// This exercises the actual server-side scheduled timeout (this repo's
	// first): place wrong, let nobody claim, and wait past StealJoinWindow.
	timeoutGuesser := currentPlayer()
	timeoutCard, err := database.GetCurrentCardAnswer(gameId)
	if err != nil {
		t.Fatalf("current card: %v", err)
	}
	timeoutTimeline, err := database.GetPlayerTimeline(gameId, timeoutGuesser.playerId)
	if err != nil {
		t.Fatalf("timeout guesser timeline: %v", err)
	}
	rec = serve(apiTrackTimeline.PlaceCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/place-card",
		url.Values{"position": {fmt.Sprint(wrongPosition(timeoutTimeline, timeoutCard.ReleaseYear))}}, timeoutGuesser.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("place card (wrong, for timeout test): %d %s", rec.Code, rec.Body.String())
	}
	if g, _ := database.GetGameById(gameId); g.RoundPhase != database.PhaseStealJoin {
		t.Fatalf("expected steal-join phase, got %q", g.RoundPhase)
	}
	for _, p := range players {
		drain(p)
	}

	time.Sleep(database.StealJoinWindow + 2*time.Second)

	sawTimeoutDiscard := false
	for _, p := range players {
		for _, pl := range resultPayloads(drain(p)) {
			if pl["type"] == "discarded" {
				sawTimeoutDiscard = true
				if pl["celebration"] != "OOF" || pl["hasGif"] != true || pl["userId"] != timeoutGuesser.userId.String() {
					t.Errorf("%s: discarded result missing %s's lose celebration: %v", p.name, timeoutGuesser.name, pl)
				}
			}
		}
	}
	if !sawTimeoutDiscard {
		t.Errorf("expected the steal-join window to time out and resolve as discarded")
	}
	if g, _ := database.GetGameById(gameId); g.RoundPhase != database.PhaseListening {
		t.Errorf("expected a fresh listening phase after the steal-join timeout resolved, got %q", g.RoundPhase)
	}

	// ================= 4c. an unclaimed window on a CORRECT original placement
	// falls back to awarding the original placer — hidden correctness ========
	// The steal-join window opens on every placement now, not just wrong ones
	// (see steal.go's doc comment): a stealer must never be able to infer
	// correctness from whether a window opens at all. This places correctly,
	// lets nobody claim, waits out the timeout, and confirms the original
	// placer still wins the card via the fallback path rather than a direct
	// resolve-immediately win.
	correctPlacer := currentPlayer()
	correctCard, err := database.GetCurrentCardAnswer(gameId)
	if err != nil {
		t.Fatalf("current card: %v", err)
	}
	correctTimeline, _ := database.GetPlayerTimeline(gameId, correctPlacer.playerId)
	preFallbackWinLen := len(correctTimeline)
	correctPos := correctPosition(correctTimeline, correctCard.ReleaseYear)

	rec = serve(apiTrackTimeline.PlaceCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/place-card",
		url.Values{"position": {fmt.Sprint(correctPos)}}, correctPlacer.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("place card (correct, hidden-correctness test): %d %s", rec.Code, rec.Body.String())
	}
	if g, _ := database.GetGameById(gameId); g.RoundPhase != database.PhaseStealJoin {
		t.Fatalf("expected a steal-join window to open even on a correct placement, got %q", g.RoundPhase)
	}
	for _, p := range players {
		drain(p)
	}

	time.Sleep(database.StealJoinWindow + 2*time.Second)

	sawFallbackWin := false
	for _, p := range players {
		for _, pl := range resultPayloads(drain(p)) {
			if pl["type"] == "won" && pl["wonByChallenge"] != true {
				sawFallbackWin = true
				if pl["userId"] != correctPlacer.userId.String() {
					t.Errorf("%s: fallback-win result userId should be the original placer, got %v", p.name, pl["userId"])
				}
				if correctPlacer.userId == players[0].userId {
					if pl["celebration"] != "GET REKT" || pl["hasGif"] != true {
						t.Errorf("%s: fallback-win missing Alice's win celebration: %v", p.name, pl)
					}
				}
			}
		}
	}
	if !sawFallbackWin {
		t.Errorf("expected the unclaimed window to fall back to the original placer's win")
	}
	postFallbackTimeline, err := database.GetPlayerTimeline(gameId, correctPlacer.playerId)
	if err != nil || len(postFallbackTimeline) != preFallbackWinLen+1 {
		t.Errorf("expected the original placer's timeline to grow on fallback win, got %d (%v)", len(postFallbackTimeline), err)
	}

	// ================= 4c2. a CORRECT exact-year lock-in still opens steal
	// when another player holds tokens — digits right must not skip the window
	exactPlacer := currentPlayer()
	exactCard, err := database.GetCurrentCardAnswer(gameId)
	if err != nil {
		t.Fatalf("current card: %v", err)
	}
	exactTokens, err := database.GetPlayerTokens(gameId, exactPlacer.playerId)
	if err != nil || exactTokens < 1 {
		t.Fatalf("exact-year placer needs a token to wager, got %d (%v)", exactTokens, err)
	}
	eligibleBefore, err := database.AnyEligibleToSteal(gameId)
	if err != nil || !eligibleBefore {
		t.Fatalf("expected another player to be eligible to steal before exact-year lock-in, eligible=%v err=%v", eligibleBefore, err)
	}

	rec = serve(apiTrackTimeline.PlaceCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/place-card",
		url.Values{
			"year":      {fmt.Sprint(exactCard.ReleaseYear)},
			"yearWager": {"1"},
		}, exactPlacer.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("place card (correct exact year): %d %s", rec.Code, rec.Body.String())
	}
	if g, _ := database.GetGameById(gameId); g.RoundPhase != database.PhaseStealJoin {
		t.Fatalf("expected steal_join after a correct exact-year lock-in with eligible stealers, got %q", g.RoundPhase)
	}
	exhaustStealWithMiss(exactPlacer, exactCard.ReleaseYear)
	for _, p := range players {
		drain(p)
	}

	// ================= 4d. a claimed steal that misses, when the original was
	// actually correct, falls back to the original placer keeping the card —
	// the stealer's spent token is lost, not refunded (stealing in vain) =====
	correctPlacer2 := currentPlayer()
	correctCard2, err := database.GetCurrentCardAnswer(gameId)
	if err != nil {
		t.Fatalf("current card: %v", err)
	}
	correctTimeline2, _ := database.GetPlayerTimeline(gameId, correctPlacer2.playerId)
	preTimelineLen2 := len(correctTimeline2)
	correctPos2 := correctPosition(correctTimeline2, correctCard2.ReleaseYear)

	rec = serve(apiTrackTimeline.PlaceCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/place-card",
		url.Values{"position": {fmt.Sprint(correctPos2)}}, correctPlacer2.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("place card (correct, steal-in-vain test): %d %s", rec.Code, rec.Body.String())
	}
	for _, p := range players {
		drain(p)
	}
	if g, _ := database.GetGameById(gameId); g.RoundPhase != database.PhaseStealJoin {
		t.Fatalf("expected a steal-join window to open, got %q", g.RoundPhase)
	}

	vainStealer := otherPlayers(correctPlacer2)[0]
	// Top up regardless of accumulated balance from earlier sections — this
	// step is testing the fallback-on-miss outcome, not token accounting.
	if err := database.SetPlayerTokens(gameId, vainStealer.playerId, 1); err != nil {
		t.Fatalf("top up vain stealer tokens: %v", err)
	}
	rec = serve(apiTrackTimeline.ClaimSteal, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/claim-steal", url.Values{}, vainStealer.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("claim steal (steal-in-vain test): %d %s", rec.Code, rec.Body.String())
	}
	for _, p := range players {
		drain(p)
	}
	vainStealerTimeline, _ := database.GetPlayerTimeline(gameId, vainStealer.playerId)
	rec = serve(apiTrackTimeline.AttemptSteal, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/attempt-steal",
		url.Values{"position": {fmt.Sprint(wrongPosition(vainStealerTimeline, correctCard2.ReleaseYear))}}, vainStealer.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("attempt steal (wrong, steal-in-vain test): %d %s", rec.Code, rec.Body.String())
	}

	sawVainFallback := false
	for _, p := range players {
		for _, pl := range resultPayloads(drain(p)) {
			if pl["type"] == "won" && pl["wonByChallenge"] != true {
				sawVainFallback = true
			}
		}
	}
	if !sawVainFallback {
		t.Errorf("expected a missed steal to fall back to the original (correct) placer's win")
	}
	postOriginalTimeline, err := database.GetPlayerTimeline(gameId, correctPlacer2.playerId)
	if err != nil || len(postOriginalTimeline) != preTimelineLen2+1 {
		t.Errorf("expected the original placer to keep the card on fallback, got %d (%v)", len(postOriginalTimeline), err)
	}
	vainStealerAfter, err := database.GetPlayerTimeline(gameId, vainStealer.playerId)
	if err != nil || len(vainStealerAfter) != len(vainStealerTimeline) {
		t.Errorf("vain stealer's timeline must not grow, got %d (%v)", len(vainStealerAfter), err)
	}
	if postVainTokens, err := database.GetPlayerTokens(gameId, vainStealer.playerId); err != nil || postVainTokens != 0 {
		t.Errorf("vain stealer's spent token should not be refunded, got %d (%v)", postVainTokens, err)
	}

	// ================= 5. guess submission earns tokens =====================
	// A new song is now in play (drawn as part of the reveal above).
	nextCard, err := database.GetCurrentCardAnswer(gameId)
	if err != nil || nextCard.CardId == uuid.Nil {
		t.Fatalf("expected a new song in play: %v", err)
	}
	guesserForTitle := otherPlayers(currentPlayer())[0]
	preTokens, err := database.GetPlayerTokens(gameId, guesserForTitle.playerId)
	if err != nil {
		t.Fatalf("pre-guess tokens: %v", err)
	}

	// The UI splits this into two fields (song name, artist) judged separately
	// so a correct artist cannot inflate the title match percent.
	rec = serve(apiTrackTimeline.SubmitGuess, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/guess",
		url.Values{"guessTitle": {nextCard.Title}, "guessArtist": {nextCard.Artist}}, guesserForTitle.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("submit guess: %d %s", rec.Code, rec.Body.String())
	}
	msgs := drain(guesserForTitle)
	alerts := alertLines(msgs)
	if len(alerts) == 0 || !strings.Contains(alerts[0], "title right") {
		t.Errorf("expected a private alert confirming a full match, got %v", alerts)
	}

	// The token is deferred to reveal (database.AwardGuessToken), not awarded
	// on submit — the balance should not have moved yet.
	postGuessTokens, err := database.GetPlayerTokens(gameId, guesserForTitle.playerId)
	if err != nil || postGuessTokens != preTokens {
		t.Errorf("guess token should not be awarded until reveal, got %d -> %d", preTokens, postGuessTokens)
	}

	// A second guess from the same player this round must be refused.
	rec = serve(apiTrackTimeline.SubmitGuess, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/guess",
		url.Values{"guessTitle": {"anything"}}, guesserForTitle.userId))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("second guess this round should be rejected, got %d", rec.Code)
	}

	// Resolve the round with a *correct* placement. A steal window now opens
	// on every placement, right or wrong, whenever anyone eligible holds a
	// token (see steal.go) — zero every other player's tokens first so no
	// window opens at all here and the round resolves immediately, cleanly
	// isolating the guess-token award (what this section is actually
	// testing) from the steal mechanic exercised elsewhere. The current
	// player never guessed this round, so guesserForTitle (the only correct
	// guess) should win the guess token regardless of who wins the card.
	resolver := currentPlayer()
	for _, p := range otherPlayers(resolver) {
		if err := database.SetPlayerTokens(gameId, p.playerId, 0); err != nil {
			t.Fatalf("zero tokens before correct resolve: %v", err)
		}
	}
	preTokens, err = database.GetPlayerTokens(gameId, guesserForTitle.playerId)
	if err != nil || preTokens != 0 {
		t.Fatalf("pre-resolve tokens: %d (%v)", preTokens, err)
	}
	resolverTimeline, _ := database.GetPlayerTimeline(gameId, resolver.playerId)
	resolverCard, err := database.GetCurrentCardAnswer(gameId)
	if err != nil {
		t.Fatalf("current card: %v", err)
	}
	rec = serve(apiTrackTimeline.PlaceCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/place-card",
		url.Values{"position": {fmt.Sprint(correctPosition(resolverTimeline, resolverCard.ReleaseYear))}}, resolver.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("place card: %d %s", rec.Code, rec.Body.String())
	}
	if g, _ := database.GetGameById(gameId); g.RoundPhase != database.PhaseReveal && g.RoundPhase != database.PhaseListening {
		t.Fatalf("expected the round to resolve immediately with nobody eligible to steal, got %q", g.RoundPhase)
	}
	for _, p := range players {
		drain(p)
	}

	postResolveTokens, err := database.GetPlayerTokens(gameId, guesserForTitle.playerId)
	if err != nil || postResolveTokens != preTokens+1 {
		t.Errorf("expected +1 token once the round resolved, got %d -> %d", preTokens, postResolveTokens)
	}

	// ================= 6. only the current player may skip, and it costs a token
	notCurrent := otherPlayers(currentPlayer())[0]
	rec = serve(apiTrackTimeline.SkipCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/skip-card", url.Values{}, notCurrent.userId))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("non-current-player skip should be rejected, got %d", rec.Code)
	}

	skipper := currentPlayer()
	// Top up regardless of how many earlier steps in this test happened to
	// spend the skipper's tokens — this step is testing skip's own cost, not
	// accumulated balance from everything before it.
	if err := database.SetPlayerTokens(gameId, skipper.playerId, 1); err != nil {
		t.Fatalf("top up skipper tokens: %v", err)
	}
	beforeSkip, _ := database.GetCurrentCard(gameId)
	rec = serve(apiTrackTimeline.SkipCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/skip-card", url.Values{}, skipper.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("skip card: %d %s", rec.Code, rec.Body.String())
	}
	afterSkip, _ := database.GetCurrentCard(gameId)
	if afterSkip.CardId == beforeSkip.CardId {
		t.Errorf("skip did not draw a replacement song")
	}
	if postSkipTokens, err := database.GetPlayerTokens(gameId, skipper.playerId); err != nil || postSkipTokens != 0 {
		t.Errorf("expected skip to spend the skipper's token (1 -> 0), got %d (%v)", postSkipTokens, err)
	}
	rec = serve(apiTrackTimeline.SkipCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/skip-card", url.Values{}, skipper.userId))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("skip with no tokens should be rejected, got %d", rec.Code)
	}
	for _, p := range players {
		drain(p)
	}

	// ================= 6b. buy a card: any player, any time during listening,
	// never touches the round already in progress ============================
	buyer := otherPlayers(currentPlayer())[0]
	if err := database.SetPlayerTokens(gameId, buyer.playerId, database.BuyCardCost); err != nil {
		t.Fatalf("top up buyer tokens: %v", err)
	}
	buyerTimelineBefore, err := database.GetPlayerTimeline(gameId, buyer.playerId)
	if err != nil {
		t.Fatalf("buyer timeline before: %v", err)
	}
	beforeBuy, err := database.GetCurrentCard(gameId)
	if err != nil {
		t.Fatalf("current card before buy: %v", err)
	}
	turnPlayerBeforeBuy := currentPlayer()

	rec = serve(apiTrackTimeline.BuyCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/buy-card", url.Values{}, buyer.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("buy card: %d %s", rec.Code, rec.Body.String())
	}
	for _, p := range players {
		drain(p)
	}

	buyerTimelineAfter, err := database.GetPlayerTimeline(gameId, buyer.playerId)
	if err != nil || len(buyerTimelineAfter) != len(buyerTimelineBefore)+1 {
		t.Errorf("expected the buyer's timeline to grow by one, got %d -> %d (%v)",
			len(buyerTimelineBefore), len(buyerTimelineAfter), err)
	}
	if postBuyTokens, err := database.GetPlayerTokens(gameId, buyer.playerId); err != nil || postBuyTokens != 0 {
		t.Errorf("expected the buy to spend %d tokens, got %d (%v)", database.BuyCardCost, postBuyTokens, err)
	}

	// A buy is a private transaction — the shared round in progress (the song
	// actually queued up, and whose turn it is) must be completely unaffected.
	afterBuy, err := database.GetCurrentCard(gameId)
	if err != nil || afterBuy.CardId != beforeBuy.CardId {
		t.Errorf("buying a card must not touch the current in-play song, got %+v -> %+v (%v)", beforeBuy, afterBuy, err)
	}
	if currentPlayer().playerId != turnPlayerBeforeBuy.playerId {
		t.Errorf("buying a card must not advance the turn")
	}
	if g, _ := database.GetGameById(gameId); g.RoundPhase != database.PhaseListening {
		t.Errorf("buying a card must not change the round phase, got %q", g.RoundPhase)
	}

	// A second attempt with no tokens left is refused.
	rec = serve(apiTrackTimeline.BuyCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/buy-card", url.Values{}, buyer.userId))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("buy with no tokens should be rejected, got %d", rec.Code)
	}

	// Buying is blocked once the UI is blocked by a timer — place wrong (with
	// tokens outstanding elsewhere) to open a steal window, then confirm a buy
	// attempt is refused while it's open, and clean the round back up.
	stealBlockPlacer := currentPlayer()
	stealBlockCard, err := database.GetCurrentCardAnswer(gameId)
	if err != nil {
		t.Fatalf("current card: %v", err)
	}
	stealBlockTimeline, _ := database.GetPlayerTimeline(gameId, stealBlockPlacer.playerId)
	someoneElse := otherPlayers(stealBlockPlacer)[0]
	if err := database.SetPlayerTokens(gameId, someoneElse.playerId, 1); err != nil {
		t.Fatalf("top up tokens for steal-block test: %v", err)
	}
	rec = serve(apiTrackTimeline.PlaceCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/place-card",
		url.Values{"position": {fmt.Sprint(wrongPosition(stealBlockTimeline, stealBlockCard.ReleaseYear))}}, stealBlockPlacer.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("place card (for buy-blocked test): %d %s", rec.Code, rec.Body.String())
	}
	for _, p := range players {
		drain(p)
	}
	if g, _ := database.GetGameById(gameId); g.RoundPhase != database.PhaseStealJoin {
		t.Fatalf("expected a steal-join window to be open")
	}
	rec = serve(apiTrackTimeline.BuyCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/buy-card", url.Values{}, someoneElse.userId))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("buying during a steal window should be rejected, got %d", rec.Code)
	}
	exhaustStealWithMiss(stealBlockPlacer, stealBlockCard.ReleaseYear)

	// ================= 7. timeout during listening: no penalty ==============
	timedOut := currentPlayer()
	preTimeline, _ := database.GetPlayerTimeline(gameId, timedOut.playerId)
	rec = serve(apiTrackTimeline.TimeoutPass, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/timeout", url.Values{}, timedOut.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("timeout: %d %s", rec.Code, rec.Body.String())
	}
	postTimeline, _ := database.GetPlayerTimeline(gameId, timedOut.playerId)
	if len(preTimeline) != len(postTimeline) {
		t.Errorf("timeout changed the player's timeline: %d -> %d", len(preTimeline), len(postTimeline))
	}
	if currentPlayer().playerId == timedOut.playerId {
		t.Errorf("turn did not advance after a listening-phase timeout")
	}
	for _, p := range players {
		found := false
		for _, l := range chatLines(drain(p)) {
			if strings.Contains(l, "ran out of time") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s did not see the timeout announcement", p.name)
		}
	}

	// ================= 8. an unattended steal turn also resolves itself ======
	// Exercises the steal_turn scheduled timeout specifically, distinct from
	// 4b's steal_join timeout: claim the sole steal attempt (which moves the
	// phase to steal_turn immediately — no queue to wait out), then never
	// submit an attempt.
	placer := currentPlayer()
	placerCard, err := database.GetCurrentCardAnswer(gameId)
	if err != nil {
		t.Fatalf("current card: %v", err)
	}
	placerTimeline, _ := database.GetPlayerTimeline(gameId, placer.playerId)
	pos := wrongPosition(placerTimeline, placerCard.ReleaseYear)

	// Eligibility to steal (and so whether a window opens at all) is checked
	// at placement time — top up a waiting stealer's tokens *before* placing,
	// regardless of how many earlier sections happened to spend it, so the
	// window is guaranteed to open rather than resolving immediately because
	// nobody else held a token.
	waitingStealer := otherPlayers(placer)[0]
	if err := database.SetPlayerTokens(gameId, waitingStealer.playerId, 1); err != nil {
		t.Fatalf("top up waiting stealer tokens: %v", err)
	}

	rec = serve(apiTrackTimeline.PlaceCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/place-card",
		url.Values{"position": {fmt.Sprint(pos)}}, placer.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("place card: %d %s", rec.Code, rec.Body.String())
	}
	for _, p := range players {
		drain(p)
	}
	if g, _ := database.GetGameById(gameId); g.RoundPhase != database.PhaseStealJoin {
		t.Fatalf("expected a steal-join window to be open")
	}

	rec = serve(apiTrackTimeline.ClaimSteal, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/claim-steal", url.Values{}, waitingStealer.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("claim steal: %d %s", rec.Code, rec.Body.String())
	}
	for _, p := range players {
		drain(p)
	}
	// The claim moves the phase to steal_turn immediately — no join window
	// left to wait out, unlike the old queue design.
	if g, _ := database.GetGameById(gameId); g.RoundPhase != database.PhaseStealTurn {
		t.Fatalf("expected the claim to open a steal_turn immediately, got %q", g.RoundPhase)
	}

	// Never submit an attempt — wait out StealTurnWindow instead.
	time.Sleep(database.StealTurnWindow + 2*time.Second)

	sawTurnTimeoutDiscard := false
	for _, p := range players {
		for _, pl := range resultPayloads(drain(p)) {
			if pl["type"] == "discarded" {
				sawTurnTimeoutDiscard = true
			}
		}
	}
	if !sawTurnTimeoutDiscard {
		t.Errorf("expected the steal_turn window to time out and resolve as discarded")
	}
	if g, _ := database.GetGameById(gameId); g.RoundPhase != database.PhaseListening {
		t.Errorf("expected a fresh listening phase after the steal_turn timeout resolved, got %q", g.RoundPhase)
	}

	// ================= 9. play out a win =====================================
	// Drive Alice specifically so her win celebration is on the payload.
	alice := players[0]
	sawGameOverCelebration := false
	sawGameOverWinVideo := false
	safety := 0
	for {
		if aliceTl, _ := database.GetPlayerTimeline(gameId, alice.playerId); len(aliceTl) >= 5 {
			break
		}
		safety++
		if safety > 200 {
			t.Fatalf("alice never reached the win condition — draw pile likely exhausted")
		}

		acting := currentPlayer()
		actingCard, err := database.GetCurrentCardAnswer(gameId)
		if err != nil || actingCard.CardId == uuid.Nil {
			t.Fatalf("no song in play mid-win-loop: %v", err)
		}
		actingTimeline, _ := database.GetPlayerTimeline(gameId, acting.playerId)

		// Alice always places correctly; everyone else always misses, so the
		// card keeps landing on Alice (directly, or by nobody else winning it
		// and the round passing back around).
		var position int
		if acting.playerId == alice.playerId {
			position = correctPosition(actingTimeline, actingCard.ReleaseYear)
		} else {
			position = wrongPosition(actingTimeline, actingCard.ReleaseYear)
		}

		rec := serve(apiTrackTimeline.PlaceCard, authedRequest(t, "POST",
			"/api/track-timeline/"+lobbyId.String()+"/place-card",
			url.Values{"position": {fmt.Sprint(position)}}, acting.userId))
		if rec.Code != http.StatusOK {
			t.Fatalf("place card in win loop: %d %s", rec.Code, rec.Body.String())
		}

		// Every placement opens a steal window if anyone else still holds a
		// token, right or wrong (see steal.go). Nobody in this loop is meant
		// to actually win a steal, so this claims-and-misses it — which falls
		// back to judging the real placement, preserving "only Alice's own
		// correct turns win" — without ever sleeping on a real timer, since
		// this loop can run many iterations.
		exhaustStealWithMiss(acting, actingCard.ReleaseYear)
		for _, p := range players {
			for _, pl := range resultPayloads(drain(p)) {
				if pl["gameOver"] == true &&
					pl["celebration"] == "GET REKT" &&
					pl["hasGif"] == true &&
					pl["userId"] == alice.userId.String() {
					sawGameOverCelebration = true
				}
				if pl["gameOver"] == true &&
					pl["winVideoId"] == "dQw4w9wgXcQ" &&
					pl["winVideoStartSeconds"] == float64(12) {
					sawGameOverWinVideo = true
				}
			}
		}
	}

	finalGame, err := database.GetGameById(gameId)
	if err != nil {
		t.Fatalf("final game state: %v", err)
	}
	if finalGame.GameStatus != database.StatusFinished {
		t.Fatalf("expected the game to be finished, got %q", finalGame.GameStatus)
	}
	if !finalGame.WinnerId.Valid || finalGame.WinnerId.UUID != alice.userId {
		t.Errorf("expected alice as the winner, got %+v", finalGame.WinnerId)
	}
	if !sawGameOverCelebration {
		t.Errorf("every client should have received Alice's win celebration on the game-over payload")
	}
	if !sawGameOverWinVideo {
		t.Errorf("every client should have received Alice's win video on the game-over payload")
	}

	// ================= 10. every restart's order differs from the last ======
	prev := strings.Join(turnOrder(), ",")
	for i := 0; i < 5; i++ {
		rec = serve(apiTrackTimeline.ResetGame, authedRequest(t, "POST",
			"/api/track-timeline/"+lobbyId.String()+"/reset", url.Values{}, players[0].userId))
		if rec.Code != http.StatusOK {
			t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
		}
		rec = serve(apiTrackTimeline.StartGame, authedRequest(t, "POST",
			"/api/track-timeline/"+lobbyId.String()+"/start",
			url.Values{}, players[0].userId))
		if rec.Code != http.StatusOK {
			t.Fatalf("restart: %d %s", rec.Code, rec.Body.String())
		}
		now := strings.Join(turnOrder(), ",")
		if now == prev {
			t.Errorf("restart %d produced the same order as the previous game: %s", i+1, now)
		}
		prev = now
		if err := gsDatabase.Execute(
			"UPDATE TRACK_TIMELINE_GAME SET GAME_STATUS = 'finished' WHERE ID = ?", gameId); err != nil {
			t.Fatalf("force finish: %v", err)
		}
		for _, p := range players {
			drain(p)
		}
	}
}
