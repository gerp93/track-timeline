package database

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// knownMetrics is every metric the builder can produce, so a catalog entry that
// names a typo'd key fails here instead of never unlocking.
func knownMetric(key string) bool {
	for _, k := range []string{
		MetricWins, MetricGames, MetricWinStreak, MetricSessionGames, MetricTableWin, MetricWorlds,
		MetricBestStreak, MetricMissStreak, MetricBounceBack, MetricStealsWon, MetricBought,
		MetricBoth, MetricBothStreak, MetricArtistOnly, MetricJasonTotal, MetricJasonRight, MetricJasonWrong,
		MetricDecades15, MetricGenres, MetricWagers, MetricWagerHits, MetricWagerStreak,
		MetricWagerBigWin, MetricWagerBigLoss, MetricWagerNetUp, MetricWagerNetDown,
	} {
		if key == k {
			return true
		}
	}
	if strings.HasPrefix(key, metricDecadePrefix) {
		return true
	}
	for _, ev := range []string{
		EvStolenFrom, EvStealDefended, EvDoubleDip, EvSpellingForgiven, EvTripleCrown, EvAllIn, EvRockBottom,
		EvNeedle, EvSoClose, EvPeakTokens, EvFlatBroke, EvNothingToLose, EvWinSpan, EvWinSeconds, EvFlawless,
		EvComeback, EvDeficit, EvPhotoFinish, EvHeist, EvFortKnox, EvChallengeRaised, EvChallengeUpheld,
		EvChallengeRejected, EvChallengeVote, EvLobbyHosted,
	} {
		if key == ev || key == ev+metricEventMaxSuffix || key == ev+metricEventMinSuffix {
			return true
		}
	}
	return false
}

func TestAchievementCatalogIsWellFormed(t *testing.T) {
	keys := make(map[string]bool)
	categories := make(map[string]bool)
	for _, c := range AchievementCategories {
		categories[c] = true
	}

	for _, a := range Achievements {
		if a.Key == "" || a.Name == "" || a.Icon == "" || a.Describe == nil {
			t.Errorf("%q is missing a field: %+v", a.Key, a)
			continue
		}
		if keys[a.Key] {
			t.Errorf("duplicate achievement key %q", a.Key)
		}
		keys[a.Key] = true
		if len(a.Key) > 48 {
			t.Errorf("%q does not fit the ACHIEVEMENT_KEY column", a.Key)
		}
		if !categories[a.Category] {
			t.Errorf("%q is in an unlisted category %q", a.Key, a.Category)
		}
		if !knownMetric(a.Metric) {
			t.Errorf("%q measures %q, which nothing produces", a.Key, a.Metric)
		}
		if len(a.Tiers) == 0 || len(a.Tiers) > len(tierLabels) && !a.Single() {
			t.Errorf("%q has %d tiers", a.Key, len(a.Tiers))
		}
		for i, threshold := range a.Tiers {
			if threshold < 1 {
				t.Errorf("%q tier %d asks for %d", a.Key, i+1, threshold)
			}
			if i == 0 {
				continue
			}
			if a.Lower && threshold >= a.Tiers[i-1] || !a.Lower && threshold <= a.Tiers[i-1] {
				t.Errorf("%q tiers %v are not getting harder", a.Key, a.Tiers)
			}
		}
		for tier := 1; tier <= len(a.Tiers); tier++ {
			if description := a.TierDescription(tier); description == "" || strings.HasSuffix(description, ".") {
				t.Errorf("%q tier %d description %q should be a sentence without a full stop", a.Key, tier, description)
			}
		}
		if a.Single() != (a.TierLabel(1) == "") {
			t.Errorf("%q: only a single achievement goes without a tier label", a.Key)
		}
	}

	if _, ok := AchievementByKey("dynasty"); !ok {
		t.Error("AchievementByKey cannot find a catalog entry")
	}
	if _, ok := AchievementByKey("nope"); ok {
		t.Error("AchievementByKey found something that is not there")
	}
}

func TestAchievementReached(t *testing.T) {
	ladder := Achievement{Metric: "x", Tiers: []int{1, 5, 10}}
	for _, c := range []struct {
		value int
		want  int
	}{{0, 0}, {1, 1}, {4, 1}, {5, 2}, {10, 3}, {99, 3}} {
		if got := ladder.Reached(Metrics{"x": c.value}); got != c.want {
			t.Errorf("value %d reached %d tiers, want %d", c.value, got, c.want)
		}
	}
	if ladder.Reached(Metrics{}) != 0 {
		t.Error("a missing metric reads as zero")
	}

	// Lower is better: a time of no more than the threshold, and no time at all is
	// not a time of zero.
	quick := Achievement{Metric: "t", Tiers: []int{1500, 1200}, Lower: true}
	for _, c := range []struct {
		m    Metrics
		want int
	}{{Metrics{}, 0}, {Metrics{"t": 1501}, 0}, {Metrics{"t": 1500}, 1}, {Metrics{"t": 1201}, 1}, {Metrics{"t": 1200}, 2}, {Metrics{"t": 3}, 2}} {
		if got := quick.Reached(c.m); got != c.want {
			t.Errorf("%v reached %d, want %d", c.m, got, c.want)
		}
	}
}

func TestGameLengthAchievementsAgree(t *testing.T) {
	// The four length achievements share two metrics; the secret ones must sit
	// beyond the visible ones so they stay the rare ones.
	quick, _ := AchievementByKey("quick_draw")
	lightning, _ := AchievementByKey("lightning")
	long, _ := AchievementByKey("long_haul")
	endurance, _ := AchievementByKey("endurance")
	if lightning.Tiers[0] >= quick.Tiers[len(quick.Tiers)-1] {
		t.Errorf("lightning (%v) must be quicker than quick draw (%v)", lightning.Tiers, quick.Tiers)
	}
	if endurance.Tiers[0] <= long.Tiers[len(long.Tiers)-1] {
		t.Errorf("endurance (%v) must be longer than long haul (%v)", endurance.Tiers, long.Tiers)
	}
	for _, a := range []Achievement{quick, lightning, long, endurance} {
		if !a.Seconds {
			t.Errorf("%s measures seconds", a.Key)
		}
	}
	if got := quick.TierDescription(1); got != "Win a 10-card game in 25 minutes or less" {
		t.Errorf("quick draw says %q", got)
	}
}

func TestPlacementYearRangeMissBy(t *testing.T) {
	r := PlacementYearRange{HasLower: true, Lower: 1980, HasUpper: true, Upper: 1990}
	for year, want := range map[int]int{1985: 0, 1980: 0, 1990: 0, 1979: 1, 1991: 1, 1975: 5, 2000: 10} {
		if got := r.MissBy(year); got != want {
			t.Errorf("MissBy(%d) = %d, want %d", year, got, want)
		}
	}
	open := PlacementYearRange{HasLower: true, Lower: 2000}
	if open.MissBy(2050) != 0 || open.MissBy(1999) != 1 {
		t.Error("an open upper end can never be missed from above")
	}
	if (PlacementYearRange{}).MissBy(1) != 0 {
		t.Error("a slot with no bounds cannot be missed")
	}
}

func TestPlacementYearRangeNarrow(t *testing.T) {
	for _, c := range []struct {
		r    PlacementYearRange
		want bool
	}{
		{PlacementYearRange{HasLower: true, Lower: 1984, HasUpper: true, Upper: 1985}, true},
		{PlacementYearRange{HasLower: true, Lower: 1984, HasUpper: true, Upper: 1984}, true},
		{PlacementYearRange{HasLower: true, Lower: 1984, HasUpper: true, Upper: 1986}, false},
		{PlacementYearRange{HasLower: true, Lower: 1984}, false},
		{PlacementYearRange{HasUpper: true, Upper: 1984}, false},
		{PlacementYearRange{}, false},
	} {
		if got := c.r.Narrow(); got != c.want {
			t.Errorf("%+v Narrow = %v, want %v", c.r, got, c.want)
		}
	}
}

// ---- metrics

var (
	ann = uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	bob = uuid.MustParse("00000000-0000-0000-0000-00000000000b")
	gus = uuid.MustParse("00000000-0000-0000-0000-00000000000c") // a guest
)

func metricsSnapshot() statsSnapshot {
	return statsSnapshot{
		users: map[uuid.UUID]statsUser{
			ann: {Name: "Ann", Approved: true},
			bob: {Name: "Bob", Approved: true},
			gus: {Name: "Guest", Approved: false},
		},
		cards: map[uuid.UUID]statsCard{},
	}
}

func TestMetricsTurnStreaksAndBounceBacks(t *testing.T) {
	s := metricsSnapshot()
	song := uuid.New()
	// hit hit hit miss miss miss hit (a bounce back) then a steal, which is not
	// part of the run, then miss miss miss miss miss (a cold snap of five).
	results := []bool{true, true, true, false, false, false, true}
	minute := 0
	for _, ok := range results {
		s.placements = append(s.placements, statsPlacement{UserId: ann, CardId: song, At: at(minute), Year: 1985, Correct: ok})
		minute++
	}
	s.placements = append(s.placements, statsPlacement{UserId: ann, CardId: song, At: at(minute), Year: 1985, Steal: true, Correct: true})
	for i := 0; i < 5; i++ {
		minute++
		s.placements = append(s.placements, statsPlacement{UserId: ann, CardId: song, At: at(minute), Year: 1985, Correct: false})
	}
	s.placements = append(s.placements, statsPlacement{UserId: gus, CardId: song, At: at(minute), Year: 1985, Correct: true})

	m := buildAchievementMetrics(s, nil, nil)
	if got := m[ann][MetricBestStreak]; got != 3 {
		t.Errorf("best streak = %d, want 3", got)
	}
	if got := m[ann][MetricMissStreak]; got != 5 {
		t.Errorf("miss streak = %d, want 5", got)
	}
	if got := m[ann][MetricBounceBack]; got != 1 {
		t.Errorf("bounce backs = %d, want 1", got)
	}
	if got := m[ann][MetricStealsWon]; got != 1 {
		t.Errorf("steals won = %d, want 1", got)
	}
	if got := m[ann][DecadeMetric(1980)]; got != 4 {
		t.Errorf("correct 1980s placements = %d, want 4 (the steal is not an own-turn placement)", got)
	}
	if _, ok := m[gus]; ok {
		t.Error("a guest has no metrics")
	}
	if len(m[bob]) != 0 {
		t.Errorf("an approved player with no history reads as empty, got %v", m[bob])
	}
}

func TestMetricsDecadesAndGenres(t *testing.T) {
	s := metricsSnapshot()
	rock, pop := uuid.New(), uuid.New()
	s.cards[rock] = statsCard{Year: 1975, Genre: "Rock / Pop"}
	s.cards[pop] = statsCard{Year: 1999, Genre: "Pop"}
	for i := 0; i < TimeTravelerPerDecade; i++ {
		s.placements = append(s.placements, statsPlacement{UserId: ann, CardId: rock, At: at(i), Year: 1975, Correct: true})
	}
	s.placements = append(s.placements,
		statsPlacement{UserId: ann, CardId: pop, At: at(100), Year: 1999, Correct: true},
		statsPlacement{UserId: ann, CardId: pop, At: at(101), Year: 1999, Correct: false},
	)

	m := buildAchievementMetrics(s, nil, nil)[ann]
	if m[DecadeMetric(1970)] != TimeTravelerPerDecade || m[DecadeMetric(1990)] != 1 {
		t.Errorf("decade counts wrong: %v", m)
	}
	if m[MetricDecades15] != 1 {
		t.Errorf("decades with %d or more = %d, want 1", TimeTravelerPerDecade, m[MetricDecades15])
	}
	if m[MetricGenres] != 2 {
		t.Errorf("genres = %d, want 2 (Rock, Pop: a compound genre counts under its first part)", m[MetricGenres])
	}
}

func TestMetricsGuessesAndAldean(t *testing.T) {
	s := metricsSnapshot()
	song := uuid.New()
	guess := func(minute int, title, artist, jason bool) {
		s.guesses = append(s.guesses, statsGuess{UserId: ann, CardId: song, At: at(minute), Title: title, Artist: artist, Jason: jason})
	}
	guess(1, true, true, false)
	guess(2, true, true, false)
	guess(3, false, true, false) // artist only: breaks the run
	guess(4, true, true, false)
	guess(5, false, true, true)  // ticked, and it was him
	guess(6, false, false, true) // ticked, and it was not

	m := buildAchievementMetrics(s, nil, nil)[ann]
	if m[MetricBoth] != 3 || m[MetricBothStreak] != 2 {
		t.Errorf("both=%d streak=%d, want 3 and 2", m[MetricBoth], m[MetricBothStreak])
	}
	if m[MetricArtistOnly] != 2 {
		t.Errorf("artist only = %d, want 2", m[MetricArtistOnly])
	}
	if m[MetricJasonTotal] != 2 || m[MetricJasonRight] != 1 || m[MetricJasonWrong] != 1 {
		t.Errorf("aldean total=%d right=%d wrong=%d, want 2/1/1", m[MetricJasonTotal], m[MetricJasonRight], m[MetricJasonWrong])
	}
}

func TestMetricsWagers(t *testing.T) {
	s := metricsSnapshot()
	song := uuid.New()
	for i, w := range []struct {
		tokens  int
		correct bool
	}{{16, false}, {4, true}, {12, true}, {7, false}, {3, true}} {
		s.wagers = append(s.wagers, statsWager{UserId: ann, CardId: song, At: at(i), Tokens: w.tokens, Correct: w.correct})
	}
	s.wagers = append(s.wagers, statsWager{UserId: bob, CardId: song, At: at(0), Tokens: 20, Correct: false})

	m := buildAchievementMetrics(s, nil, nil)
	a := m[ann]
	if a[MetricWagers] != 5 || a[MetricWagerHits] != 3 {
		t.Errorf("wagers=%d hits=%d, want 5 and 3", a[MetricWagers], a[MetricWagerHits])
	}
	if a[MetricWagerStreak] != 2 {
		t.Errorf("wager streak = %d, want 2", a[MetricWagerStreak])
	}
	if a[MetricWagerBigWin] != 12 || a[MetricWagerBigLoss] != 16 {
		t.Errorf("big win=%d loss=%d, want 12 and 16", a[MetricWagerBigWin], a[MetricWagerBigLoss])
	}
	// +4 +12 +3 -16 -7 = -4
	if a[MetricWagerNetDown] != 4 || a[MetricWagerNetUp] != 0 {
		t.Errorf("net down=%d up=%d, want 4 and 0", a[MetricWagerNetDown], a[MetricWagerNetUp])
	}
	if m[bob][MetricWagerNetDown] != 20 {
		t.Errorf("bob net down = %d, want 20", m[bob][MetricWagerNetDown])
	}
}

func TestMetricsGames(t *testing.T) {
	s := metricsSnapshot()
	game := func(minute int, winner uuid.UUID, players int, room sql.NullBool, who ...uuid.UUID) StatsGame {
		return StatsGame{WonOn: at(minute), WinnerId: winner, PlayerCount: players, Room: room, Players: who}
	}
	online, inPerson := sql.NullBool{Bool: false, Valid: true}, sql.NullBool{Bool: true, Valid: true}
	games := []StatsGame{
		game(0, ann, 2, online, ann, bob),
		game(40, ann, 5, online, ann, bob),         // same sitting: 40 minutes later
		game(60*5, bob, 2, inPerson, ann, bob),     // five hours later: a new sitting
		game(60*5+30, ann, 1, sql.NullBool{}, ann), // a solo game counts for nothing
		game(60*5+45, ann, 2, inPerson, ann, bob),
	}

	m := buildAchievementMetrics(s, games, nil)
	a, b := m[ann], m[bob]
	if a[MetricGames] != 4 || a[MetricWins] != 3 {
		t.Errorf("ann games=%d wins=%d, want 4 and 3 (the solo game does not count)", a[MetricGames], a[MetricWins])
	}
	if a[MetricWinStreak] != 2 {
		t.Errorf("ann win streak = %d, want 2 (a loss in the third game ended the first run)", a[MetricWinStreak])
	}
	if b[MetricGames] != 4 || b[MetricWins] != 1 || b[MetricWinStreak] != 1 {
		t.Errorf("bob games=%d wins=%d streak=%d", b[MetricGames], b[MetricWins], b[MetricWinStreak])
	}
	if a[MetricSessionGames] != 2 {
		t.Errorf("ann's longest sitting = %d games, want 2", a[MetricSessionGames])
	}
	if a[MetricTableWin] != 5 {
		t.Errorf("biggest table won = %d, want 5", a[MetricTableWin])
	}
	if a[MetricWorlds] != 2 || b[MetricWorlds] != 1 {
		t.Errorf("worlds ann=%d bob=%d, want 2 and 1", a[MetricWorlds], b[MetricWorlds])
	}
}

func TestMetricsEventsAndBought(t *testing.T) {
	s := metricsSnapshot()
	s.events = []statsCardEvent{
		{Type: CardEventBought, UserId: uuid.NullUUID{UUID: ann, Valid: true}},
		{Type: CardEventBought, UserId: uuid.NullUUID{UUID: ann, Valid: true}},
		{Type: CardEventBought}, // an unattributed buy is nobody's
		{Type: CardEventDealt, UserId: uuid.NullUUID{UUID: ann, Valid: true}},
	}
	events := userEventTotals{
		ann: {
			EvPeakTokens: {Count: 3, Max: 31, Min: 16},
			EvWinSeconds: {Count: 2, Max: 2700, Min: 1180},
		},
		gus: {EvStolenFrom: {Count: 9, Max: 1, Min: 1}},
	}

	m := buildAchievementMetrics(s, nil, events)
	a := m[ann]
	if a[MetricBought] != 2 {
		t.Errorf("bought = %d, want 2", a[MetricBought])
	}
	if a[EvPeakTokens+metricEventMaxSuffix] != 31 || a[EvWinSeconds+metricEventMinSuffix] != 1180 || a[EvPeakTokens] != 3 {
		t.Errorf("event metrics wrong: %v", a)
	}
	if _, ok := m[gus]; ok {
		t.Error("a guest's events earn nothing")
	}

	// The catalog reads those back: 1180s is under both quick draw rungs, and the
	// best peak of 31 clears the first two hoard rungs.
	quick, _ := AchievementByKey("quick_draw")
	hoard, _ := AchievementByKey("dragon_hoard")
	if quick.Reached(a) != 2 || hoard.Reached(a) != 2 {
		t.Errorf("quick draw reached %d (want 2), dragon hoard %d (want 2)", quick.Reached(a), hoard.Reached(a))
	}
}

// ---- trophy view

func noUnlocks(int) (time.Time, bool, bool) { return time.Time{}, false, false }

func TestBuildTrophyProgress(t *testing.T) {
	dynasty, _ := AchievementByKey("dynasty")

	// Three wins unlocked two rungs; the bar is toward the third (10).
	earned := func(tier int) (time.Time, bool, bool) { return at(tier), tier == 1, tier <= 2 }
	trophy := buildTrophy(dynasty, Metrics{MetricWins: 4}, earned)
	if trophy.Reached != 2 || trophy.Medal() != "Silver" || trophy.Complete {
		t.Fatalf("reached=%d medal=%q complete=%v", trophy.Reached, trophy.Medal(), trophy.Complete)
	}
	if !trophy.HasBar || trophy.Progress != "4 / 10" || trophy.Percent != 40 {
		t.Errorf("progress %q (%d%%, bar %v), want 4 / 10 at 40%%", trophy.Progress, trophy.Percent, trophy.HasBar)
	}
	if !trophy.Rungs[0].Backfilled || trophy.Rungs[1].Backfilled || trophy.Rungs[2].Unlocked {
		t.Errorf("rungs wrong: %+v", trophy.Rungs)
	}

	// What was unlocked stays unlocked even if the number later dips.
	if got := buildTrophy(dynasty, Metrics{}, earned); got.Reached != 2 {
		t.Errorf("a dipped metric took a trophy back: reached %d", got.Reached)
	}

	// A finished ladder has nothing left to chase.
	done := buildTrophy(dynasty, Metrics{MetricWins: 99}, func(int) (time.Time, bool, bool) { return at(0), false, true })
	if !done.Complete || done.HasBar || done.Progress != "" || done.Next != "" {
		t.Errorf("a complete ladder should show no progress: %+v", done)
	}
}

func TestBuildTrophySingleAndTimed(t *testing.T) {
	heist, _ := AchievementByKey("the_heist")
	got := buildTrophy(heist, Metrics{}, noUnlocks)
	if got.Unlocked() || got.HasBar || got.Progress != "" || !got.Single {
		t.Errorf("a locked single of 1 has no bar: %+v", got)
	}

	cold, _ := AchievementByKey("cold_snap")
	got = buildTrophy(cold, Metrics{MetricMissStreak: 3}, noUnlocks)
	if !got.HasBar || got.Progress != "3 / 5" {
		t.Errorf("a single that asks for 5 shows how close: %+v", got)
	}

	quick, _ := AchievementByKey("quick_draw")
	if got := buildTrophy(quick, Metrics{}, noUnlocks); got.Progress != "no time yet · aim for 25:00" || got.HasBar {
		t.Errorf("no time yet reads %q", got.Progress)
	}
	long, _ := AchievementByKey("long_haul")
	if got := buildTrophy(long, Metrics{}, noUnlocks); got.Progress != "no time yet · aim for 40:00" || got.HasBar {
		t.Errorf("a slow game not yet won reads %q, not a time of 0:00", got.Progress)
	}
	if got := buildTrophy(long, Metrics{EvWinSeconds + metricEventMaxSuffix: 1800}, noUnlocks); got.Progress != "30:00 / 40:00" || got.Percent != 75 {
		t.Errorf("a 30-minute game on the way to 40 reads %q at %d%%", got.Progress, got.Percent)
	}
	got = buildTrophy(quick, Metrics{EvWinSeconds + metricEventMinSuffix: 1800}, noUnlocks)
	if got.Progress != "best 30:00 · aim for 25:00" || !got.HasBar || got.Percent != 83 {
		t.Errorf("a time reads %q at %d%%", got.Progress, got.Percent)
	}
}

func TestUnlockTitle(t *testing.T) {
	if got := (AchievementUnlock{Name: "Highway Robber", TierLabel: "Gold"}).Title(); got != "Highway Robber (Gold)" {
		t.Errorf("title = %q", got)
	}
	if got := (AchievementUnlock{Name: "The Heist"}).Title(); got != "The Heist" {
		t.Errorf("a single achievement carries no tier, got %q", got)
	}
}
