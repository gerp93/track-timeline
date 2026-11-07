package main

import (
	"os"
	"strconv"
	"strings"
	"testing"

	gsAuth "github.com/gerp93/gameshell-framework/auth"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
)

// The lobby's trophy drawer lists what was unlocked during this game, by any of
// its players, and nothing else. Needs the same throwaway tt_e2e database as the
// other end-to-end tests.
func TestGameTrophiesAgainstARealDatabase(t *testing.T) {
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

	makeUser := func(name string) uuid.UUID {
		t.Helper()
		if err := gsDatabase.CreateUser(name, "unused-not-a-login", true); err != nil {
			t.Logf("create user %s (may already exist): %v", name, err)
		}
		id, err := gsDatabase.GetUserIdByName(name)
		if err != nil {
			t.Fatalf("get user %s: %v", name, err)
		}
		return id
	}
	carol := makeUser("trophy_carol")
	dave := makeUser("trophy_dave")
	outsider := makeUser("trophy_outsider")

	lobbyId, err := database.CreateLobby("trophy lobby "+uuid.NewString(), "", "")
	if err != nil {
		t.Fatalf("create lobby: %v", err)
	}
	for _, u := range []uuid.UUID{carol, dave} {
		if err := gsDatabase.AddUserLobbyAccess(u, lobbyId); err != nil {
			t.Fatalf("lobby access: %v", err)
		}
		if _, err := gsDatabase.AddUserToLobby(lobbyId, u); err != nil {
			t.Fatalf("join lobby: %v", err)
		}
	}
	gameId, err := database.CreateGame(lobbyId, database.StandardCardsToWin, 4, false, false, database.PlaybackIntro, 20)
	if err != nil {
		t.Fatalf("create game: %v", err)
	}

	unlock := func(user uuid.UUID, key string, tier int, ago string, backfilled bool) {
		t.Helper()
		flag := 0
		if backfilled {
			flag = 1
		}
		if err := gsDatabase.Execute(`
			INSERT INTO TRACK_TIMELINE_ACHIEVEMENT_UNLOCK (ID, CREATED_ON_DATE, USER_ID, ACHIEVEMENT_KEY, TIER, BACKFILLED)
			VALUES (UUID(), NOW() - INTERVAL ` + ago + `, '` + user.String() + `', '` + key + `', ` + strconv.Itoa(tier) + `, ` + strconv.Itoa(flag) + `)
			ON DUPLICATE KEY UPDATE CREATED_ON_DATE = VALUES(CREATED_ON_DATE), BACKFILLED = VALUES(BACKFILLED)`); err != nil {
			t.Fatalf("unlock %s/%d: %v", key, tier, err)
		}
	}

	// Before the game starts, nothing in it has been won -- even if players
	// unlocked things a moment ago.
	unlock(carol, "on_fire", 1, "5 SECOND", false)
	if got, err := database.GetGameTrophies(gameId); err != nil || len(got) != 0 {
		t.Fatalf("a game that has not started has no trophies, got %+v (err=%v)", got, err)
	}

	if err := gsDatabase.Execute("UPDATE TRACK_TIMELINE_GAME SET STARTED_ON_DATE = NOW() - INTERVAL 10 MINUTE WHERE ID = '" + gameId.String() + "'"); err != nil {
		t.Fatalf("stamp start: %v", err)
	}
	// Re-stamp every unlock relative to the start.
	unlock(carol, "on_fire", 1, "5 MINUTE", false)    // won in this game, by carol
	unlock(dave, "on_fire", 1, "2 MINUTE", false)     // won in this game, by dave (newest)
	unlock(carol, "on_fire", 2, "3 MINUTE", false)    // a later tier of the same ladder
	unlock(carol, "all_in", 1, "1 HOUR", false)       // before the game started
	unlock(dave, "dynasty", 1, "1 MINUTE", true)      // backfilled history, not won
	unlock(outsider, "on_fire", 1, "1 MINUTE", false) // not a player in this game

	got, err := database.GetGameTrophies(gameId)
	if err != nil {
		t.Fatalf("get trophies: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 trophies (dave's bronze, carol's silver, carol's bronze), got %d: %+v", len(got), got)
	}
	want := []struct{ player, title string }{
		{"trophy_dave", "On Fire (Bronze)"},
		{"trophy_carol", "On Fire (Silver)"},
		{"trophy_carol", "On Fire (Bronze)"},
	}
	for i, w := range want {
		if got[i].UserName != w.player || got[i].Title() != w.title {
			t.Errorf("trophy %d = %s / %s, want %s / %s (newest first)", i, got[i].UserName, got[i].Title(), w.player, w.title)
		}
	}
	if got[0].Description != "Place 5 songs correctly in a row on your turn" || got[0].TierClass() != "bronze" {
		t.Errorf("trophy describes itself as %q / %q", got[0].Description, got[0].TierClass())
	}

	// A reset game has no start time and so no trophies, until it starts again.
	if err := gsDatabase.Execute("UPDATE TRACK_TIMELINE_GAME SET STARTED_ON_DATE = NULL WHERE ID = '" + gameId.String() + "'"); err != nil {
		t.Fatalf("clear start: %v", err)
	}
	if got, err := database.GetGameTrophies(gameId); err != nil || len(got) != 0 {
		t.Errorf("a reset game has no trophies, got %+v (err=%v)", got, err)
	}
}
