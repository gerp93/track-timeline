package main

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	gsAuth "github.com/gerp93/gameshell-framework/auth"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
)

// The achievements engine against a real database: unlocks are worked out from
// the logs, written once, announced once, silently backfilled for history, and
// shown (or hidden) on the trophy case. Needs the same throwaway tt_e2e database
// as the other end-to-end tests.
func TestAchievementsAgainstARealDatabase(t *testing.T) {
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

	makeUser := func(name string, approved bool) uuid.UUID {
		t.Helper()
		if err := gsDatabase.CreateUser(name, "unused-not-a-login", approved); err != nil {
			t.Logf("create user %s (may already exist): %v", name, err)
		}
		id, err := gsDatabase.GetUserIdByName(name)
		if err != nil {
			t.Fatalf("get user %s: %v", name, err)
		}
		return id
	}
	alice := makeUser("ach_alice", true)
	bob := makeUser("ach_bob", true)
	guest := makeUser("ach_guest", false)

	unlockOf := func(unlocks []database.AchievementUnlock, userId uuid.UUID, key string) (database.AchievementUnlock, bool) {
		for _, u := range unlocks {
			if u.UserId == userId && u.Key == key {
				return u, true
			}
		}
		return database.AchievementUnlock{}, false
	}
	evaluate := func(userIds ...uuid.UUID) []database.AchievementUnlock {
		t.Helper()
		unlocks, err := database.EvaluateAchievements(userIds)
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		return unlocks
	}

	// ---- a ladder climbs one rung at a time, and a rung is announced once ------
	song := uuid.New()
	for i := 0; i < 5; i++ {
		if err := database.LogPlacement(alice, song, 1985, false, true); err != nil {
			t.Fatalf("log placement: %v", err)
		}
	}
	first := evaluate(alice)
	fire, ok := unlockOf(first, alice, "on_fire")
	if !ok || fire.Tier != 1 || fire.TierLabel != "Bronze" || fire.TierCount != 4 || fire.UserName != "ach_alice" {
		t.Fatalf("five in a row should unlock On Fire bronze, got %+v", first)
	}
	if fire.Description != "Place 5 songs correctly in a row on your turn" || fire.Title() != "On Fire (Bronze)" {
		t.Errorf("unlock describes itself as %q / %q", fire.Description, fire.Title())
	}
	if again := evaluate(alice); len(again) != 0 {
		t.Errorf("a tier already unlocked must not be announced again, got %+v", again)
	}

	for i := 0; i < 3; i++ {
		if err := database.LogPlacement(alice, song, 1985, false, true); err != nil {
			t.Fatalf("log placement: %v", err)
		}
	}
	second := evaluate(alice)
	if fire, ok := unlockOf(second, alice, "on_fire"); !ok || fire.Tier != 2 || fire.TierLabel != "Silver" {
		t.Fatalf("eight in a row should unlock On Fire silver, got %+v", second)
	}

	// ---- a single achievement from an event, and a guest earns nothing --------
	if err := database.LogAchievementEvent(alice, uuid.Nil, database.EvAllIn, 1); err != nil {
		t.Fatalf("log event: %v", err)
	}
	if err := database.LogAchievementEvent(guest, uuid.Nil, database.EvAllIn, 1); err != nil {
		t.Fatalf("log event: %v", err)
	}
	third := evaluate(alice, guest)
	allIn, ok := unlockOf(third, alice, "all_in")
	if !ok || allIn.TierLabel != "" || allIn.Title() != "All In" {
		t.Fatalf("an all-in event should unlock All In (a single, with no tier), got %+v", third)
	}
	if _, ok := unlockOf(third, guest, "all_in"); ok {
		t.Error("a guest cannot earn achievements")
	}

	// ---- history is backfilled silently ---------------------------------------
	if err := database.LogWin(bob, 10, 3, sql.NullBool{Bool: false, Valid: true}); err != nil {
		t.Fatalf("log win: %v", err)
	}
	if err := database.BackfillAchievements(); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if after := evaluate(bob); len(after) != 0 {
		t.Errorf("what was backfilled must not be announced afterwards, got %+v", after)
	}
	var backfilled int
	rows, err := gsDatabase.Query(
		"SELECT COUNT(*) FROM TRACK_TIMELINE_ACHIEVEMENT_UNLOCK WHERE USER_ID = ? AND ACHIEVEMENT_KEY = 'dynasty' AND TIER = 1 AND BACKFILLED = 1", bob)
	if err != nil {
		t.Fatalf("read backfilled unlocks: %v", err)
	}
	if rows.Next() {
		_ = rows.Scan(&backfilled)
	}
	rows.Close()
	if backfilled != 1 {
		t.Errorf("bob's earlier win should be a backfilled Dynasty tier 1, found %d", backfilled)
	}

	// ---- game-scoped questions -------------------------------------------------
	lobbyId, err := database.CreateLobby("achievements lobby", "", "")
	if err != nil {
		t.Fatalf("create lobby: %v", err)
	}
	gameId, err := database.CreateGame(lobbyId, database.StandardCardsToWin, 4, false, false, database.PlaybackIntro, 20)
	if err != nil {
		t.Fatalf("create game: %v", err)
	}
	if _, ok, err := database.GameSeconds(gameId); err != nil || ok {
		t.Errorf("a game that has not started has no running time (ok=%v err=%v)", ok, err)
	}
	if _, ok, err := database.GameOwnTurnMisses(alice, gameId); err != nil || ok {
		t.Errorf("a game that has not started has no misses to count (ok=%v err=%v)", ok, err)
	}
	if err := gsDatabase.Execute("UPDATE TRACK_TIMELINE_GAME SET STARTED_ON_DATE = NOW() - INTERVAL 125 SECOND WHERE ID = '" + gameId.String() + "'"); err != nil {
		t.Fatalf("stamp start: %v", err)
	}
	if seconds, ok, err := database.GameSeconds(gameId); err != nil || !ok || seconds < 125 || seconds > 130 {
		t.Errorf("running time = %d (ok=%v err=%v), want about 125", seconds, ok, err)
	}
	// A miss before the game began is not this game's.
	if err := gsDatabase.Execute(`
		INSERT INTO TRACK_TIMELINE_LOG_PLACEMENT (ID, CREATED_ON_DATE, USER_ID, CARD_ID, RELEASE_YEAR, IS_CHALLENGE, IS_CORRECT)
		VALUES (UUID(), NOW() - INTERVAL 1 HOUR, '` + alice.String() + `', UUID(), 1990, 0, 0)`); err != nil {
		t.Fatalf("log an old miss: %v", err)
	}
	if misses, ok, err := database.GameOwnTurnMisses(alice, gameId); err != nil || !ok || misses != 0 {
		t.Errorf("misses = %d (ok=%v err=%v), want 0: the old miss belongs to an earlier game", misses, ok, err)
	}
	if err := database.LogPlacement(alice, song, 1985, false, false); err != nil {
		t.Fatalf("log a miss: %v", err)
	}
	if err := database.LogPlacement(alice, song, 1985, true, false); err != nil {
		t.Fatalf("log a missed steal: %v", err)
	}
	if misses, _, _ := database.GameOwnTurnMisses(alice, gameId); misses != 1 {
		t.Errorf("misses = %d, want 1: a missed steal is not an own-turn miss", misses)
	}

	// A peak from the game before this one (the lobby was reset and restarted)
	// is not this game's.
	if err := gsDatabase.Execute(`
		INSERT INTO TRACK_TIMELINE_LOG_ACHIEVEMENT_EVENT (ID, CREATED_ON_DATE, USER_ID, GAME_ID, EVENT_TYPE, VALUE)
		VALUES (UUID(), NOW() - INTERVAL 1 HOUR, '` + alice.String() + `', '` + gameId.String() + `', 'peak_tokens', 99)`); err != nil {
		t.Fatalf("log an old peak: %v", err)
	}
	if n, _ := database.CountGameEvents(alice, gameId, database.EvPeakTokens); n != 0 {
		t.Errorf("an event from before the game started counted: %d", n)
	}
	for _, v := range []int{16, 23, 21} {
		if err := database.LogAchievementEvent(alice, gameId, database.EvPeakTokens, v); err != nil {
			t.Fatalf("log peak: %v", err)
		}
	}
	if n, _ := database.CountGameEvents(alice, gameId, database.EvPeakTokens); n != 3 {
		t.Errorf("peak events = %d, want 3", n)
	}
	if peak, _ := database.MaxGameEventValue(alice, gameId, database.EvPeakTokens); peak != 23 {
		t.Errorf("peak = %d, want 23", peak)
	}
	if peak, _ := database.MaxGameEventValue(bob, gameId, database.EvPeakTokens); peak != 0 {
		t.Errorf("an event nobody logged for bob reads %d, want 0", peak)
	}
	if unlocks := evaluate(alice); func() bool { _, ok := unlockOf(unlocks, alice, "dragon_hoard"); return !ok }() {
		t.Errorf("a balance of 23 should unlock Dragon Hoard bronze, got %+v", unlocks)
	}

	// A game won in record time: fastest and slowest both read the same events.
	for _, seconds := range []int{19 * 60, 41 * 60} {
		if err := database.LogAchievementEvent(bob, gameId, database.EvWinSeconds, seconds); err != nil {
			t.Fatalf("log win time: %v", err)
		}
	}
	times := evaluate(bob)
	for _, key := range []string{"quick_draw", "long_haul"} {
		if _, ok := unlockOf(times, bob, key); !ok {
			t.Errorf("a 19-minute and a 41-minute win should unlock %s, got %+v", key, times)
		}
	}
	if quick, _ := unlockOf(times, bob, "quick_draw"); quick.Tier != 2 {
		t.Errorf("19 minutes clears both quick draw rungs, got tier %d", quick.Tier)
	}
	if _, ok := unlockOf(times, bob, "lightning"); ok {
		t.Error("19 minutes is not under 15: lightning stays locked")
	}

	// ---- the trophy case -------------------------------------------------------
	trophyCase, found, err := database.GetTrophyCase(alice)
	if err != nil || !found {
		t.Fatalf("alice's trophy case: found=%v err=%v", found, err)
	}
	var onFire *database.Trophy
	for ci := range trophyCase.Categories {
		for ti := range trophyCase.Categories[ci].Trophies {
			if trophyCase.Categories[ci].Trophies[ti].Key == "on_fire" {
				onFire = &trophyCase.Categories[ci].Trophies[ti]
			}
		}
	}
	if onFire == nil || onFire.Reached != 2 || onFire.Medal() != "Silver" || onFire.Progress != "8 / 12" {
		t.Fatalf("On Fire should read silver with 8 / 12 to gold, got %+v", onFire)
	}
	secrets := 0
	for _, a := range database.Achievements {
		if a.Secret {
			secrets++
		}
	}
	if trophyCase.HiddenSecrets != secrets {
		t.Errorf("alice has unlocked no secrets, so all %d should be hidden, got %d", secrets, trophyCase.HiddenSecrets)
	}
	if trophyCase.Earned < 3 || trophyCase.Total != len(database.Achievements)-secrets {
		t.Errorf("earned=%d total=%d, want at least 3 earned of %d visible", trophyCase.Earned, trophyCase.Total, len(database.Achievements)-secrets)
	}
	if _, found, _ := database.GetTrophyCase(guest); found {
		t.Error("a guest has no trophy case")
	}
}
