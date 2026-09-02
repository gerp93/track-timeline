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

	deckId, err := gsDatabase.CreateDeck("e2e deck", "", true)
	if err != nil {
		t.Fatalf("create deck: %v", err)
	}
	for i := 0; i < 60; i++ {
		year := sql.NullInt64{Int64: int64(1000 + i*10), Valid: true}
		// The deck+video-id pair is unique, so each card needs its own
		// 11-character id even though none of them is ever really played.
		videoId := fmt.Sprintf("e2eVideo%03d", i)
		_, err := database.CreateCard(deckId, videoId, 0,
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
	gameId, err := database.CreateGame(lobbyId, 5, 2)
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

	// ================= 2. wrong placement opens a challenge window ==========
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
	// — it should be waiting in the challenge phase.
	g, err := database.GetGameById(gameId)
	if err != nil {
		t.Fatalf("get game: %v", err)
	}
	if g.RoundPhase != database.PhaseChallenge {
		t.Fatalf("expected challenge phase after a wrong placement with tokens outstanding, got %q", g.RoundPhase)
	}
	sawChallengeStatus := false
	for _, p := range players {
		for _, l := range statusLines(drain(p)) {
			if strings.Contains(l, "challenge") {
				sawChallengeStatus = true
			}
		}
	}
	if !sawChallengeStatus {
		t.Errorf("expected a status line announcing the challenge window")
	}

	// ================= 3. challenging your own placement is refused =========
	rec = serve(apiTrackTimeline.Challenge, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/challenge",
		url.Values{"position": {"0"}}, guesser.userId))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("guesser challenging their own placement should be rejected, got %d", rec.Code)
	}

	// ================= 4. a correct challenge steals the card ===============
	challengers := otherPlayers(guesser)
	challenger := challengers[0]
	bystander := challengers[1]

	challengerTimeline, err := database.GetPlayerTimeline(gameId, challenger.playerId)
	if err != nil || len(challengerTimeline) != 1 {
		t.Fatalf("challenger timeline: %d (%v)", len(challengerTimeline), err)
	}
	rightPos := correctPosition(challengerTimeline, card.ReleaseYear)

	rec = serve(apiTrackTimeline.Challenge, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/challenge",
		url.Values{"position": {fmt.Sprint(rightPos)}}, challenger.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("challenge (correct): %d %s", rec.Code, rec.Body.String())
	}
	for _, p := range players {
		drain(p)
	}
	postChallengeTokens, err := database.GetPlayerTokens(gameId, challenger.playerId)
	if err != nil || postChallengeTokens != 1 {
		t.Errorf("challenger should have spent a token (2 -> 1), got %d (%v)", postChallengeTokens, err)
	}

	// The window is still open (bystander has not acted) — force it closed
	// rather than waiting on a player who has decided not to challenge, the
	// same way a real UI button or timeout would.
	rec = serve(apiTrackTimeline.CloseChallenge, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/close-challenge", url.Values{}, bystander.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("close challenge: %d %s", rec.Code, rec.Body.String())
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
			}
		}
	}
	if !sawSteal {
		t.Errorf("expected a won/wonByChallenge result after the challenge window closed")
	}

	postSteal, err := database.GetPlayerTimeline(gameId, challenger.playerId)
	if err != nil || len(postSteal) != 2 {
		t.Errorf("challenger should have won the card (timeline 1 -> 2), got %d (%v)", len(postSteal), err)
	}
	guesserAfter, err := database.GetPlayerTimeline(gameId, guesser.playerId)
	if err != nil || len(guesserAfter) != 1 {
		t.Errorf("original guesser's timeline must not grow on a lost steal, got %d (%v)", len(guesserAfter), err)
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

	// "Title by Artist", not "Artist - Title": the judge's normalizer treats a
	// " - " separator as a featured-artist/remaster clause and strips
	// everything after it (see guess/normalized.go stripFeaturing), so an
	// "Artist - Title" guess would only ever be judged on the artist half.
	rec = serve(apiTrackTimeline.SubmitGuess, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/guess",
		url.Values{"guess": {nextCard.Title + " by " + nextCard.Artist}}, guesserForTitle.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("submit guess: %d %s", rec.Code, rec.Body.String())
	}
	msgs := drain(guesserForTitle)
	alerts := alertLines(msgs)
	if len(alerts) == 0 || !strings.Contains(alerts[0], "Title and artist both right") {
		t.Errorf("expected a private alert confirming a full match, got %v", alerts)
	}
	postTokens, err := database.GetPlayerTokens(gameId, guesserForTitle.playerId)
	if err != nil || postTokens != preTokens+2 {
		t.Errorf("expected +2 tokens for a correct title+artist guess, got %d -> %d", preTokens, postTokens)
	}
	sawTokenAnnounce := false
	for _, p := range otherPlayers(guesserForTitle) {
		for _, l := range chatLines(drain(p)) {
			if strings.Contains(l, "earned") && strings.Contains(l, "token") {
				sawTokenAnnounce = true
				if strings.Contains(l, nextCard.Title) || strings.Contains(l, nextCard.Artist) {
					t.Errorf("token-earned chat leaked the answer: %s", l)
				}
			}
		}
	}
	if !sawTokenAnnounce {
		t.Errorf("expected the lobby to hear that a guess earned tokens")
	}

	// A second guess from the same player this round must be refused.
	rec = serve(apiTrackTimeline.SubmitGuess, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/guess",
		url.Values{"guess": {"anything"}}, guesserForTitle.userId))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("second guess this round should be rejected, got %d", rec.Code)
	}

	// ================= 6. only the current player may skip ==================
	notCurrent := otherPlayers(currentPlayer())[0]
	rec = serve(apiTrackTimeline.SkipCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/skip-card", url.Values{}, notCurrent.userId))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("non-current-player skip should be rejected, got %d", rec.Code)
	}

	skipper := currentPlayer()
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
	for _, p := range players {
		drain(p)
	}

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

	// ================= 8. timeout during a challenge window resolves ========
	placer := currentPlayer()
	placerCard, err := database.GetCurrentCardAnswer(gameId)
	if err != nil {
		t.Fatalf("current card: %v", err)
	}
	placerTimeline, _ := database.GetPlayerTimeline(gameId, placer.playerId)
	pos := wrongPosition(placerTimeline, placerCard.ReleaseYear)
	rec = serve(apiTrackTimeline.PlaceCard, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/place-card",
		url.Values{"position": {fmt.Sprint(pos)}}, placer.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("place card: %d %s", rec.Code, rec.Body.String())
	}
	for _, p := range players {
		drain(p)
	}
	if g, _ := database.GetGameById(gameId); g.RoundPhase != database.PhaseChallenge {
		t.Fatalf("expected a challenge window to be open")
	}
	rec = serve(apiTrackTimeline.TimeoutPass, authedRequest(t, "POST",
		"/api/track-timeline/"+lobbyId.String()+"/timeout", url.Values{}, placer.userId))
	if rec.Code != http.StatusOK {
		t.Fatalf("timeout during challenge: %d %s", rec.Code, rec.Body.String())
	}
	sawRevealAfterTimeout := false
	for _, p := range players {
		for _, pl := range resultPayloads(drain(p)) {
			if pl["type"] == "won" || pl["type"] == "discarded" {
				sawRevealAfterTimeout = true
			}
		}
	}
	if !sawRevealAfterTimeout {
		t.Errorf("expected the challenge window to resolve when time ran out")
	}
	if g, _ := database.GetGameById(gameId); g.RoundPhase != database.PhaseListening {
		t.Errorf("expected a fresh listening phase after the challenge window resolved, got %q", g.RoundPhase)
	}

	// ================= 9. play out a win =====================================
	// Drive Alice specifically so her win celebration is on the payload.
	alice := players[0]
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

		// Close out any challenge window immediately — nobody in this loop is
		// meant to be stealing, only Alice's own correct turns should win.
		if g, _ := database.GetGameById(gameId); g.RoundPhase == database.PhaseChallenge {
			serve(apiTrackTimeline.CloseChallenge, authedRequest(t, "POST",
				"/api/track-timeline/"+lobbyId.String()+"/close-challenge", url.Values{}, alice.userId))
		}
		for _, p := range players {
			drain(p)
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

	// ================= 10. every restart's order differs from the last ======
	prev := strings.Join(turnOrder(), ",")
	for i := 0; i < 5; i++ {
		rec = serve(apiTrackTimeline.ResetGame, authedRequest(t, "POST",
			"/api/track-timeline/"+lobbyId.String()+"/reset", url.Values{}, players[0].userId))
		if rec.Code != http.StatusOK {
			t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
		}
		rec = serve(apiTrackTimeline.StartGame, authedRequest(t, "POST",
			"/api/track-timeline/"+lobbyId.String()+"/start", url.Values{}, players[0].userId))
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
