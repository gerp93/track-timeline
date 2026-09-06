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
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	apiRoom "github.com/gerp93/track-timeline/api/room"
	apiTrackTimeline "github.com/gerp93/track-timeline/api/tracktimeline"
	"github.com/gerp93/track-timeline/database"
)

// TestRoomModeEndToEnd covers the same-room vertical slice: seatless host,
// guest + account join, lobby-search exclusion, host-disconnect pause, and
// turn-only guessing. Remote /track-timeline lobbies are left alone.
func TestRoomModeEndToEnd(t *testing.T) {
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

	stamp := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	hostName := "room_host_" + stamp
	if err := gsDatabase.CreateUser(hostName, "unused-not-a-login", true); err != nil {
		t.Fatalf("create host user: %v", err)
	}
	hostUserId, err := gsDatabase.GetUserIdByName(hostName)
	if err != nil {
		t.Fatalf("get host user: %v", err)
	}

	deckId, err := gsDatabase.CreateDeck("room deck "+stamp, "", true)
	if err != nil {
		t.Fatalf("create deck: %v", err)
	}
	if err := gsDatabase.AddUserDeckAccess(hostUserId, deckId); err != nil {
		t.Fatalf("grant deck: %v", err)
	}
	for i := 0; i < 20; i++ {
		year := sql.NullInt64{Int64: int64(1970 + i), Valid: true}
		_, err := database.CreateCard(
			deckId,
			fmt.Sprintf("roomVid%02d%s", i, stamp[len(stamp)-4:]),
			fmt.Sprintf("Room Song %d", i),
			fmt.Sprintf("Room Artist %d", i),
			year,
			uuid.NullUUID{},
		)
		if err != nil {
			t.Fatalf("create card %d: %v", i, err)
		}
	}

	form := url.Values{}
	form.Set("name", "Room Night "+stamp)
	form.Set("cardsToWin", "5")
	form.Set("startingTokens", "2")
	form.Set("playbackMode", database.PlaybackSample)
	form.Set("guessMode", database.GuessModeBoth)
	form.Set("clipSeconds", "20")
	form.Add("deckId", deckId.String())

	createRec := serve(apiRoom.Create, authedRequest(t, "POST", "/api/room/create", form, hostUserId))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create room: status %d body %q", createRec.Code, createRec.Body.String())
	}
	redirect := createRec.Header().Get("HX-Redirect")
	if !strings.HasPrefix(redirect, "/room/") || !strings.HasSuffix(redirect, "/host") {
		t.Fatalf("create room redirect = %q", redirect)
	}
	code := strings.TrimSuffix(strings.TrimPrefix(redirect, "/room/"), "/host")

	var hostCookie *http.Cookie
	for _, c := range createRec.Result().Cookies() {
		if c.Name == "TRACK-TIMELINE-ROOM-HOST" {
			hostCookie = c
			break
		}
	}
	if hostCookie == nil || hostCookie.Value == "" {
		t.Fatal("create room did not set host cookie")
	}
	if hostCookie.MaxAge < 11*60*60 {
		t.Fatalf("host cookie MaxAge=%d, want ~12h for a night of play", hostCookie.MaxAge)
	}

	room, err := database.GetRoomByCode(code)
	if err != nil {
		t.Fatalf("GetRoomByCode: %v", err)
	}
	if room.CreatorUserId != hostUserId {
		t.Fatalf("creator = %s, want %s", room.CreatorUserId, hostUserId)
	}

	hostPlayer, err := gsDatabase.GetLobbyUserPlayer(room.LobbyId, hostUserId)
	if err != nil {
		t.Fatalf("GetLobbyUserPlayer host: %v", err)
	}
	if hostPlayer.Id != uuid.Nil {
		t.Fatalf("host was seated as player %s; host display must be seatless", hostPlayer.Id)
	}

	isRoom, err := database.LobbyIsRoom(room.LobbyId)
	if err != nil || !isRoom {
		t.Fatalf("LobbyIsRoom = %v, %v", isRoom, err)
	}

	found, err := database.SearchLobbies("Room Night "+stamp, 1)
	if err != nil {
		t.Fatalf("SearchLobbies: %v", err)
	}
	for _, lob := range found {
		if lob.Id == room.LobbyId {
			t.Fatalf("SearchLobbies returned room lobby %s", lob.Id)
		}
	}

	guestForm := url.Values{}
	guestForm.Set("name", "Sam")
	guestReq := httptest.NewRequest("POST", "/api/room/"+code+"/join-guest", strings.NewReader(guestForm.Encode()))
	guestReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	guestReq.SetPathValue("code", code)
	guestRec := serve(apiRoom.JoinGuest, guestReq)
	if guestRec.Code != http.StatusOK {
		t.Fatalf("join guest: status %d body %q", guestRec.Code, guestRec.Body.String())
	}
	if !strings.Contains(guestRec.Header().Get("HX-Redirect"), "/room/"+code+"/play") {
		t.Fatalf("join guest redirect = %q", guestRec.Header().Get("HX-Redirect"))
	}

	var guestUserId uuid.UUID
	for _, c := range guestRec.Result().Cookies() {
		probe := httptest.NewRequest("GET", "/", nil)
		probe.AddCookie(c)
		if id, err := gsAuth.GetUserId(probe); err == nil && id != uuid.Nil {
			guestUserId = id
			break
		}
	}
	if guestUserId == uuid.Nil {
		t.Fatal("join guest did not set auth cookie")
	}
	guestPlayer, err := gsDatabase.GetLobbyUserPlayer(room.LobbyId, guestUserId)
	if err != nil || guestPlayer.Id == uuid.Nil {
		t.Fatalf("guest not seated: %v player=%+v", err, guestPlayer)
	}

	accountName := "room_seat_" + stamp
	if err := gsDatabase.CreateUser(accountName, "unused-not-a-login", true); err != nil {
		t.Fatalf("create seat user: %v", err)
	}
	seatUserId, err := gsDatabase.GetUserIdByName(accountName)
	if err != nil {
		t.Fatalf("get seat user: %v", err)
	}
	joinAcc := authedRequest(t, "POST", "/api/room/"+code+"/join-account", nil, seatUserId)
	joinAcc.SetPathValue("code", code)
	joinAccRec := serve(apiRoom.JoinAccount, joinAcc)
	if joinAccRec.Code != http.StatusOK {
		t.Fatalf("join account: status %d body %q", joinAccRec.Code, joinAccRec.Body.String())
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/room/{code}", apiRoom.ServeWs)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	hostHdr := http.Header{}
	hostHdr.Add("Cookie", hostCookie.Name+"="+hostCookie.Value)
	hostConn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/room/"+code+"?role=host", hostHdr)
	if err != nil {
		t.Fatalf("host ws dial: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	room, err = database.GetRoomByCode(code)
	if err != nil {
		t.Fatalf("reload room: %v", err)
	}
	if room.IsPaused {
		t.Fatal("room still paused after host connected")
	}
	_ = hostConn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		room, err = database.GetRoomByCode(code)
		if err != nil {
			t.Fatalf("reload room after host close: %v", err)
		}
		if room.IsPaused {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("room did not pause after host disconnect")
		}
		time.Sleep(50 * time.Millisecond)
	}

	hostConn2, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/room/"+code+"?role=host", hostHdr)
	if err != nil {
		t.Fatalf("host ws redial: %v", err)
	}
	defer hostConn2.Close()
	time.Sleep(300 * time.Millisecond)
	room, err = database.GetRoomByCode(code)
	if err != nil {
		t.Fatalf("reload room after host reconnect: %v", err)
	}
	if room.IsPaused {
		t.Fatal("room still paused after host reconnect")
	}

	startRec := serve(
		apiTrackTimeline.StartGame,
		authedRequest(t, "POST", "/api/track-timeline/"+room.LobbyId.String()+"/start", nil, guestUserId),
	)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start game: status %d body %q", startRec.Code, startRec.Body.String())
	}
	game, err := database.GetGame(room.LobbyId)
	if err != nil {
		t.Fatalf("GetGame: %v", err)
	}
	tokens, err := database.GetPlayerTokens(game.Id, guestPlayer.Id)
	if err != nil {
		t.Fatalf("GetPlayerTokens: %v", err)
	}
	if tokens != 2 {
		t.Fatalf("starting tokens = %d, want 2", tokens)
	}

	currentId := uuid.Nil
	if game.CurrentPlayerId.Valid {
		currentId = game.CurrentPlayerId.UUID
	}
	nonTurnUser := seatUserId
	if currentId != guestPlayer.Id {
		nonTurnUser = guestUserId
	}
	guessForm := url.Values{}
	guessForm.Set("guessTitle", "Room Song 0")
	guessForm.Set("guessArtist", "Room Artist 0")
	guessRec := serve(
		apiTrackTimeline.SubmitGuess,
		authedRequest(t, "POST", "/api/track-timeline/"+room.LobbyId.String()+"/guess", guessForm, nonTurnUser),
	)
	if guessRec.Code == http.StatusOK {
		t.Fatalf("non-turn room guess should be rejected, got 200: %q", guessRec.Body.String())
	}
	if !strings.Contains(strings.ToLower(guessRec.Body.String()), "turn") {
		t.Fatalf("non-turn rejection body = %q", guessRec.Body.String())
	}
}
