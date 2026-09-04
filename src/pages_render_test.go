package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	gsApi "github.com/gerp93/gameshell-framework/api"
	gsApiPages "github.com/gerp93/gameshell-framework/api/pages"
	gsAuth "github.com/gerp93/gameshell-framework/auth"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	"github.com/google/uuid"

	apiPages "github.com/gerp93/track-timeline/api/pages"
	"github.com/gerp93/track-timeline/database"
)

// Regression test for the shared-page-template pattern this repo follows.
// These handlers all discard ExecuteTemplate's error (`_ = tmpl.ExecuteTemplate(...)`),
// so a template referencing a field the Go data struct doesn't have fails
// *silently* — a 200 with a truncated body, not a 500. Asserting on real
// rendered content (not just status code) is the only way to catch that.
func TestSharedPageTemplatesRender(t *testing.T) {
	if !strings.HasPrefix(os.Getenv("TRACK_TIMELINE_SQL_DATABASE"), "tt_e2e") {
		t.Skip("set TRACK_TIMELINE_SQL_DATABASE=tt_e2e")
	}
	gsDatabase.SetEnvVarPrefix("TRACK_TIMELINE")
	gsAuth.SetCookiePrefix("TRACK-TIMELINE")
	if _, err := gsDatabase.CreateDatabaseConnection(); err != nil {
		t.Fatalf("db: %v", err)
	}
	setupSchema(t)
	gsApiPages.SetAccountPageFeatures(gsApiPages.AccountPageFeatures{WinCelebration: true, LoseCelebration: true})

	if err := gsDatabase.CreateUser("render_admin", "unused", true); err != nil {
		t.Logf("create user (may already exist): %v", err)
	}
	userId, err := gsDatabase.GetUserIdByName("render_admin")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if err := gsDatabase.SetUserIsAdmin(userId, true); err != nil {
		t.Fatalf("set admin: %v", err)
	}

	deckId, err := gsDatabase.CreateDeck("render deck", "", true)
	if err != nil {
		t.Fatalf("create deck: %v", err)
	}
	categoryId, err := database.CreateCategory("Render Genre")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	year := sql.NullInt64{Int64: 1969, Valid: true}
	cardId, err := database.CreateCard(deckId, "renderVideoId", "Render Song", "Render Artist",
		year, uuid.NullUUID{UUID: categoryId, Valid: true})
	if err != nil {
		t.Fatalf("create card: %v", err)
	}

	// Give the stats pages something to render: a win, a correct placement,
	// and a correct guess.
	if err := database.LogWin(userId, 10, 2); err != nil {
		t.Fatalf("log win: %v", err)
	}
	if err := database.LogPlacement(userId, cardId, 1969, false, true); err != nil {
		t.Fatalf("log placement: %v", err)
	}
	if err := database.LogTitleGuess(userId, cardId, "render song by render artist", true, true); err != nil {
		t.Fatalf("log guess: %v", err)
	}
	if err := gsDatabase.AddUserDeckAccess(userId, deckId); err != nil {
		t.Fatalf("grant deck access: %v", err)
	}

	// Set up a lobby + game so TrackTimelineLobbies / TrackTimelineLobby
	// render real content too.
	lobbyId, err := database.CreateLobby("render lobby", "", "")
	if err != nil {
		t.Fatalf("create lobby: %v", err)
	}
	if err := gsDatabase.AddUserLobbyAccess(userId, lobbyId); err != nil {
		t.Fatalf("grant lobby access: %v", err)
	}
	if _, err := gsDatabase.AddUserToLobby(lobbyId, userId); err != nil {
		t.Fatalf("join lobby: %v", err)
	}
	gameId, err := database.CreateGame(lobbyId, 10, 2, database.GuessModeBoth, database.DefaultGuessMatchPercent, database.GuessJudgeLocal, database.PlaybackSample, 20)
	if err != nil {
		t.Fatalf("create game: %v", err)
	}
	if err := database.InitializeDrawPile(gameId, []uuid.UUID{deckId}, nil); err != nil {
		t.Fatalf("init draw pile: %v", err)
	}

	cookieRec := httptest.NewRecorder()
	gsAuth.SetUserId(cookieRec, userId)
	cookies := cookieRec.Result().Cookies()

	get := func(target string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		return r
	}

	cases := []struct {
		name      string
		handler   http.HandlerFunc
		path      string
		anonymous bool
		setPath   func(r *http.Request)
		want      []string
	}{
		{"Login", gsApiPages.Login, "/login", true, nil, []string{"User Login"}},
		{"Users", gsApiPages.Users, "/users", false, nil, []string{"render_admin"}},
		{"Decks", apiPages.Decks, "/decks", false, nil, []string{"render deck", "Library Issues"}},
		{"Account", gsApiPages.Account, "/account", false, nil, []string{"Win Celebration", "render_admin"}},
		{"Categories", apiPages.Categories, "/categories", false, nil, []string{"Render Genre"}},
		{"DeadVideos", apiPages.DeadVideos, "/videos", false, nil, []string{"Library", "Dead Videos (", "Duplicates (", "Ungenred ("}},
		{"GuessTest", apiPages.GuessTest, "/guess-test", false, nil, []string{"Quizmaster Testing", "Find song", "Heuristic match required", "Claude config", "claude-haiku-4-5"}},
		{
			"Deck", apiPages.Deck, "/deck/{deckId}", false,
			func(r *http.Request) { r.SetPathValue("deckId", deckId.String()) },
			[]string{"render deck", "Render Song", "Render Artist", "1969", "Render Genre"},
		},
		{
			"TrackTimelineLobbies", apiPages.TrackTimelineLobbies, "/track-timeline/lobbies", false, nil,
			[]string{"render deck", "Render Genre", "AI Judge"},
		},
		{
			"TrackTimelineLobby", apiPages.TrackTimelineLobby, "/track-timeline/{lobbyId}", false,
			func(r *http.Request) { r.SetPathValue("lobbyId", lobbyId.String()) },
			[]string{"render lobby"},
		},
		{"Stats", apiPages.Stats, "/stats", false, nil, []string{"Leaderboard", "Hardest Songs"}},
		{"StatsLeaderboard", apiPages.StatsLeaderboard, "/stats/leaderboard", false, nil, []string{"render_admin", "1"}},
		{"StatsUsers", apiPages.StatsUsers, "/stats/users", false, nil, []string{"render_admin"}},
		{
			"StatsUser", apiPages.StatsUser, "/stats/user/{userId}", false,
			func(r *http.Request) { r.SetPathValue("userId", userId.String()) },
			[]string{"render_admin", "Wins", "1"},
		},
		{"StatsCards", apiPages.StatsCards, "/stats/cards", false, nil, []string{"Hardest Songs"}},
		{
			"StatsCard", apiPages.StatsCard, "/stats/card/{cardId}", false,
			func(r *http.Request) { r.SetPathValue("cardId", cardId.String()) },
			[]string{"Render Song", "Render Artist", "1969"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req *http.Request
			if tc.anonymous {
				req = httptest.NewRequest(http.MethodGet, tc.path, nil)
			} else {
				req = get(tc.path)
			}
			if tc.setPath != nil {
				tc.setPath(req)
			}
			rec := httptest.NewRecorder()
			gsApi.MiddlewareForPages(tc.handler).ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("response missing %q; template may have failed silently. Full body:\n%s", want, body)
				}
			}
		})
	}
}
