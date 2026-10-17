package database

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
)

var statsEpoch = time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC)

func at(minutes int) time.Time { return statsEpoch.Add(time.Duration(minutes) * time.Minute) }

func TestRateBasics(t *testing.T) {
	var empty Rate
	if empty.Has() || empty.Percent() != "–" || empty.Fraction() != "" || empty.Width() != 0 {
		t.Fatalf("empty rate should render as nothing: %+v", empty)
	}
	r := Rate{Hit: 3, Total: 4}
	if r.Percent() != "75%" || r.Fraction() != "3 / 4" || r.Width() != 75 || r.Misses() != 1 {
		t.Fatalf("unexpected rendering of 3/4: %s %s %d", r.Percent(), r.Fraction(), r.Width())
	}
	if !r.Small() || (Rate{Hit: 5, Total: SmallSample}).Small() {
		t.Fatal("Small should be true below SmallSample and false at it")
	}
}

func TestRateIntervalStaysInBoundsAndShrinksWithSample(t *testing.T) {
	// The textbook normal interval for 10/10 is a single point at 100%, and for
	// 0/3 it dips below zero; Wilson must do neither.
	for _, r := range []Rate{{10, 10}, {0, 3}, {1, 1}, {0, 1}, {50, 100}} {
		lo, hi := r.Interval()
		if lo < 0 || hi > 100 || lo > hi {
			t.Fatalf("%+v interval out of bounds: %.1f-%.1f", r, lo, hi)
		}
	}
	if lo, _ := (Rate{10, 10}).Interval(); lo >= 100 {
		t.Fatalf("a perfect 10/10 should still leave room for doubt, lo=%.1f", lo)
	}
	loSmall, hiSmall := (Rate{5, 10}).Interval()
	loBig, hiBig := (Rate{500, 1000}).Interval()
	if hiSmall-loSmall <= hiBig-loBig {
		t.Fatal("a bigger sample must give a tighter interval")
	}
	// Known value: 50/100 -> about 40.4-59.6.
	lo, hi := (Rate{50, 100}).Interval()
	if math.Abs(lo-40.4) > 0.2 || math.Abs(hi-59.6) > 0.2 {
		t.Fatalf("Wilson 50/100 = %.1f-%.1f, want ~40.4-59.6", lo, hi)
	}
}

func TestSmoothedShrinksTowardThePrior(t *testing.T) {
	// One miss in one try is not "the hardest song"; it should sit between 0 and
	// the prior, and a long record of misses should be pulled less.
	one := Rate{0, 1}.Smoothed(0.7, 4)
	many := Rate{0, 20}.Smoothed(0.7, 4)
	if !(0 < many && many < one && one < 0.7) {
		t.Fatalf("shrinkage ordering wrong: one=%.3f many=%.3f", one, many)
	}
	if got := (Rate{}).Smoothed(0.7, 4); math.Abs(got-0.7) > 1e-9 {
		t.Fatalf("no data should equal the prior, got %.3f", got)
	}
}

func TestMedian(t *testing.T) {
	cases := []struct {
		in   []int
		want int
	}{{nil, 0}, {[]int{7}, 7}, {[]int{3, 1, 2}, 2}, {[]int{10, 20, 30, 40}, 25}, {[]int{1, 2}, 2}}
	for _, c := range cases {
		if got := median(c.in); got != c.want {
			t.Errorf("median(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestReconstructGamesSplitsAtWinsAndCapsLookback(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	place := func(u uuid.UUID, m int) statsPlacement { return statsPlacement{UserId: u, At: at(m), Correct: true} }

	placements := []statsPlacement{
		// An abandoned game from long before: no win row, must not leak into game 1.
		place(c, 0),
		// Game 1: a and b.
		place(a, 400), place(b, 405), place(a, 410),
		// Game 2: starts right after game 1's win.
		place(b, 421), place(a, 425), place(b, 430),
	}
	wins := []statsWin{
		{UserId: a, At: at(411), CardsToWin: 10, PlayerCount: 2},
		{UserId: b, At: at(431), CardsToWin: 10, PlayerCount: 2},
	}

	games := reconstructGames(wins, placements)
	if len(games) != 2 {
		t.Fatalf("got %d games, want 2", len(games))
	}
	if games[0].Placements != 3 || len(games[0].Players) != 2 {
		t.Fatalf("game 1 should hold 3 placements by 2 players, got %+v", games[0])
	}
	for _, p := range games[0].Players {
		if p == c {
			t.Fatal("the abandoned game's player leaked into game 1")
		}
	}
	if games[0].Minutes != 11 {
		t.Fatalf("game 1 minutes = %d, want 11 (400 -> 411)", games[0].Minutes)
	}
	if games[1].Placements != 3 || games[1].WinnerId != b {
		t.Fatalf("game 2 wrong: %+v", games[1])
	}
	if games[0].Solo() || !((StatsGame{PlayerCount: 1}).Solo()) {
		t.Fatal("Solo should mean a table of one")
	}
}

func TestReconstructGamesTreatsALongSilenceAsARestart(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	place := func(u uuid.UUID, m int) statsPlacement { return statsPlacement{UserId: u, At: at(m), Correct: true} }

	// A lobby plays 20 minutes (c is in it), is reset, and a new game is played
	// and won by a, with no win logged for the first attempt.
	placements := []statsPlacement{
		place(a, 0), place(b, 2), place(c, 4), place(a, 19),
		place(a, 42), place(b, 43), place(a, 45), place(b, 50), // 23 minutes of silence before this game
	}
	games := reconstructGames([]statsWin{{UserId: a, At: at(51), CardsToWin: 10, PlayerCount: 2}}, placements)
	if len(games) != 1 {
		t.Fatalf("got %d games, want 1", len(games))
	}
	if games[0].Placements != 4 || len(games[0].Players) != 2 {
		t.Fatalf("only the restarted game's placements should count, got %+v", games[0])
	}
	if games[0].Minutes != 9 {
		t.Fatalf("minutes = %d, want 9 (42 -> 51), not the whole evening", games[0].Minutes)
	}
	for _, p := range games[0].Players {
		if p == c {
			t.Fatal("a player who only appeared in the unfinished game must not be seated in the next one")
		}
	}

	// A pause shorter than the idle gap is a break, not a restart.
	shortBreak := []statsPlacement{place(a, 0), place(b, 1), place(a, 12), place(b, 13)}
	games = reconstructGames([]statsWin{{UserId: a, At: at(14), CardsToWin: 10, PlayerCount: 2}}, shortBreak)
	if games[0].Placements != 4 || games[0].Minutes != 14 {
		t.Fatalf("a 11-minute pause must not split a game, got %+v", games[0])
	}
}

func TestReconstructGamesAlwaysSeatsTheWinner(t *testing.T) {
	winner := uuid.New()
	games := reconstructGames([]statsWin{{UserId: winner, At: at(5), PlayerCount: 2}}, nil)
	if len(games) != 1 || len(games[0].Players) != 1 || games[0].Players[0] != winner || games[0].Minutes != 0 {
		t.Fatalf("a win with no placements should still seat the winner: %+v", games)
	}
}

func TestLongestStreaksIgnoreStealsAndResetOnAMiss(t *testing.T) {
	a := uuid.New()
	p := func(steal, correct bool) statsPlacement {
		return statsPlacement{UserId: a, Steal: steal, Correct: correct}
	}
	seq := []statsPlacement{
		p(false, true), p(false, true), p(true, false), // a failed steal must not break the run
		p(false, true), p(false, false), p(false, true),
	}
	if got := longestStreaks(seq)[a]; got != 3 {
		t.Fatalf("longest streak = %d, want 3", got)
	}
}

func TestBuildSessionsSplitsOnGapAndCountsGames(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	placements := []statsPlacement{
		{UserId: a, At: at(0), Correct: true}, {UserId: b, At: at(30), Correct: false},
		{UserId: a, At: at(60), Steal: true, Correct: true},
		{UserId: b, At: at(60 + 5*60), Correct: true}, // five hours later: a new session
	}
	wins := []statsWin{{UserId: a, At: at(61)}, {UserId: b, At: at(60 + 5*60 + 1)}}
	sessions := buildSessions(placements, wins)
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(sessions))
	}
	if sessions[0].Placements != 3 || sessions[0].Players != 2 || sessions[0].Games != 1 {
		t.Fatalf("session 1 wrong: %+v", sessions[0])
	}
	if sessions[0].Turn != (Rate{Hit: 1, Total: 2}) {
		t.Fatalf("session 1 turn accuracy should leave the steal out: %+v", sessions[0].Turn)
	}
	if sessions[1].Games != 1 {
		t.Fatalf("session 2 wrong: %+v", sessions[1])
	}
}

func TestBuildStatsReport(t *testing.T) {
	ann, bob, guest, gone := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	songA, songB, songDeleted := uuid.New(), uuid.New(), uuid.New()

	snapshot := statsSnapshot{
		users: map[uuid.UUID]statsUser{
			ann:   {Name: "Ann", Approved: true},
			bob:   {Name: "Bob", Approved: true},
			guest: {Name: "guest-7", Approved: false},
		},
		cards: map[uuid.UUID]statsCard{
			songA: {Title: "A Song", Artist: "A Band", Year: 1975, Genre: "Pop / Rock"},
			songB: {Title: "B Song", Artist: "B Band", Year: 1994, Genre: "Pop"},
		},
		placements: []statsPlacement{
			{UserId: ann, CardId: songA, At: at(0), Year: 1975, Correct: true},
			{UserId: bob, CardId: songB, At: at(2), Year: 1994, Correct: false},
			{UserId: ann, CardId: songB, At: at(3), Year: 1994, Steal: true, Correct: true},
			{UserId: guest, CardId: songA, At: at(5), Year: 1975, Correct: true},
			{UserId: gone, CardId: songDeleted, At: at(7), Year: 1985, Correct: false},
			{UserId: bob, CardId: songA, At: at(9), Year: 1975, Correct: true},
		},
		wins: []statsWin{{UserId: ann, At: at(10), CardsToWin: 10, PlayerCount: 2}},
		guesses: []statsGuess{
			{UserId: ann, CardId: songA, Title: true, Artist: true},
			{UserId: bob, CardId: songA, Title: true, Artist: false},
			{UserId: bob, CardId: songB, Title: false, Artist: false},
		},
		events: []statsCardEvent{
			{songA, CardEventDrawn}, {songA, CardEventDrawn}, {songB, CardEventDrawn},
			{songB, CardEventSkipped}, {songA, CardEventBought}, {songDeleted, CardEventDiscarded},
			{songA, CardEventDealt},
		},
	}
	report := buildStatsReport(snapshot)

	// Table-wide totals include the guest and the deleted account.
	if report.Totals.Placements != 6 || report.Totals.Songs != 3 {
		t.Fatalf("totals wrong: %+v", report.Totals)
	}
	if report.Totals.Turn != (Rate{Hit: 3, Total: 5}) || report.Totals.Steal != (Rate{Hit: 1, Total: 1}) {
		t.Fatalf("turn/steal split wrong: turn %+v steal %+v", report.Totals.Turn, report.Totals.Steal)
	}
	if report.Totals.Draws != 3 || report.Totals.Skips != 1 || report.Totals.Bought != 1 || report.Totals.Discards != 1 {
		t.Fatalf("card events wrong: %+v", report.Totals)
	}
	if report.Totals.Games != 1 || report.Totals.SoloGames != 0 {
		t.Fatalf("games wrong: %+v", report.Totals)
	}

	// Only approved accounts get a player row.
	if len(report.Players) != 2 || report.Players[0].UserName != "Ann" || report.Players[1].UserName != "Bob" {
		t.Fatalf("players wrong: %+v", report.Players)
	}
	annStats, bobStats := report.Players[0], report.Players[1]
	if annStats.Wins != 1 || annStats.Games != 1 || annStats.Rank != 1 || bobStats.Rank != 2 {
		t.Fatalf("win accounting wrong: ann %+v bob %+v", annStats, bobStats)
	}
	if math.Abs(annStats.Expected-0.5) > 1e-9 || math.Abs(annStats.Edge()-0.5) > 1e-9 || annStats.EdgeLabel() != "+0.5" || bobStats.EdgeLabel() != "-0.5" {
		t.Fatalf("expected-wins maths wrong: ann %v/%s bob %s", annStats.Expected, annStats.EdgeLabel(), bobStats.EdgeLabel())
	}
	if annStats.Steal != (Rate{Hit: 1, Total: 1}) || bobStats.Turn != (Rate{Hit: 1, Total: 2}) {
		t.Fatalf("player turn/steal wrong: ann %+v bob %+v", annStats, bobStats)
	}
	if annStats.Both != (Rate{Hit: 1, Total: 1}) || bobStats.Title != (Rate{Hit: 1, Total: 2}) {
		t.Fatalf("guess accounting wrong: ann %+v bob %+v", annStats.Both, bobStats.Title)
	}
	if len(annStats.Versus) != 1 || annStats.Versus[0].OpponentName != "Bob" || annStats.Versus[0].Wins != 1 || bobStats.Versus[0].Losses != 1 {
		t.Fatalf("head-to-head wrong: %+v / %+v", annStats.Versus, bobStats.Versus)
	}

	// Decade axis is gap-free and lines up with each player's cells.
	wantAxis := []int{1970, 1980, 1990}
	if len(report.DecadeAxis) != 3 || report.DecadeAxis[0] != wantAxis[0] || report.DecadeAxis[2] != wantAxis[2] {
		t.Fatalf("decade axis wrong: %v", report.DecadeAxis)
	}
	if annStats.Decades[0] != (Rate{Hit: 1, Total: 1}) || annStats.Decades[1].Has() {
		t.Fatalf("ann's decade cells wrong: %+v", annStats.Decades)
	}

	// Genres fold compound names to the first part and need enough placements.
	if len(report.Genres) != 0 {
		t.Fatalf("genres below the minimum sample must be hidden, got %+v", report.Genres)
	}

	// Songs: the deleted one keeps its stats but not its name; guest placements count.
	byId := map[uuid.UUID]SongStats{}
	for _, s := range report.Songs {
		byId[s.CardId] = s
	}
	if s := byId[songDeleted]; s.Known || s.Title != "(deleted song)" || s.Turn != (Rate{Hit: 0, Total: 1}) {
		t.Fatalf("deleted song wrong: %+v", s)
	}
	if s := byId[songA]; s.Turn != (Rate{Hit: 3, Total: 3}) || s.Guesses != 2 || s.Bought != 1 || s.Draws != 2 {
		t.Fatalf("song A wrong: %+v", s)
	}
	if s := byId[songA]; s.Recognised != (Rate{Hit: 3, Total: 4}) {
		t.Fatalf("recognition should pool title and artist: %+v", s.Recognised)
	}
	// Song A (3/3) outranks nothing but is easier than songs with < minSongTries: only A is rankable.
	if byId[songA].Percentile != 0 || byId[songB].Percentile != -1 {
		t.Fatalf("percentiles wrong: A=%d B=%d", byId[songA].Percentile, byId[songB].Percentile)
	}
	if len(byId[songA].Attempts) != 3 || byId[songA].Attempts[1].UserName != "Guest" {
		t.Fatalf("attempt names wrong: %+v", byId[songA].Attempts)
	}
}

func TestLeaderboardRankingSharesPositionsOnlyWhenLevel(t *testing.T) {
	a := PlayerStats{Wins: 3, Games: 6}
	b := PlayerStats{Wins: 3, Games: 6}
	c := PlayerStats{Wins: 3, Games: 4}
	d := PlayerStats{Wins: 1, Games: 1}
	if leaderboardBefore(a, b) || leaderboardBefore(b, a) {
		t.Fatal("level players must not outrank each other")
	}
	if !leaderboardBefore(c, a) {
		t.Fatal("same wins in fewer games (better win rate) should rank higher")
	}
	if !leaderboardBefore(a, d) {
		t.Fatal("more wins must beat a better rate on fewer wins")
	}
}

func TestSongViewOrdersByShrunkDifficulty(t *testing.T) {
	report := &StatsReport{TurnAccuracy: 0.7}
	mk := func(title string, hit, total int) SongStats {
		s := SongStats{Known: true, Title: title, Turn: Rate{Hit: hit, Total: total}}
		s.Smoothed = s.Turn.Smoothed(report.TurnAccuracy, smoothingStrength)
		return s
	}
	// "Unlucky" missed all 3 tries (100% raw); "Brutal" missed 8 of 10 (80% raw).
	// The longer record is stronger evidence of difficulty and must rank harder
	// even though its raw rate is better.
	report.Songs = []SongStats{mk("Unlucky", 0, 3), mk("Brutal", 2, 10), mk("Easy", 5, 5), mk("Untried", 1, 2)}
	hardest := songView(report, SongViewHardest)
	if len(hardest) != 2 || hardest[0].Title != "Brutal" || hardest[1].Title != "Unlucky" {
		t.Fatalf("hardest order wrong: %+v", hardest)
	}
	// "Easy" is the only song above the table average, so it alone is "easiest";
	// nothing above average may leak into the hardest list or vice versa.
	easiest := songView(report, SongViewEasiest)
	if len(easiest) != 1 || easiest[0].Title != "Easy" {
		t.Fatalf("easiest order wrong: %+v", easiest)
	}
}

func TestHeatCellShadesOnlyRealGapsFromTheTable(t *testing.T) {
	table := Rate{Hit: 70, Total: 100}
	cases := []struct {
		name  string
		cell  HeatCell
		class string
		tint  int
		sub   string
	}{
		{"clearly better", HeatCell{Rate{Hit: 9, Total: 10}, table}, "stat-heat-up", 80, "+20 \u00b7 10"},
		{"clearly worse", HeatCell{Rate{Hit: 4, Total: 10}, table}, "stat-heat-down", 100, "-30 \u00b7 10"},
		{"within three points", HeatCell{Rate{Hit: 7, Total: 10}, table}, "", 0, "\u00b10 \u00b7 10"},
		{"too few tries to call", HeatCell{Rate{Hit: 2, Total: 2}, table}, "", 0, "2"},
		{"no table to compare with", HeatCell{Rate{Hit: 9, Total: 10}, Rate{}}, "", 0, "9"},
	}
	for _, c := range cases {
		if got := c.cell.Class(); got != c.class {
			t.Errorf("%s: class = %q, want %q", c.name, got, c.class)
		}
		if got := c.cell.Tint(); got != c.tint {
			t.Errorf("%s: tint = %d, want %d", c.name, got, c.tint)
		}
	}
	if got := cases[0].cell.Sub(); got != cases[0].sub {
		t.Errorf("sub = %q, want %q", got, cases[0].sub)
	}
	if got := cases[3].cell.Sub(); got != "2" {
		t.Errorf("a cell too small to compare should print just its count, got %q", got)
	}
}

func TestBuildStatsReportWithNoData(t *testing.T) {
	report := buildStatsReport(statsSnapshot{
		users: map[uuid.UUID]statsUser{},
		cards: map[uuid.UUID]statsCard{},
	})
	if report.Totals.Placements != 0 || len(report.Players) != 0 || len(report.Songs) != 0 || len(report.Eras) != 0 {
		t.Fatalf("an empty log should give an empty report: %+v", report.Totals)
	}
	if report.Totals.Turn.Has() || report.Totals.Turn.Percent() != "–" {
		t.Fatal("an empty table should have no accuracy to show")
	}
	if len(report.AxisLabels()) != 0 {
		t.Fatal("no decades without placements")
	}
}

func TestRecognitionViewsSplitAtTheTableAverage(t *testing.T) {
	report := &StatsReport{TurnAccuracy: 0.7, Totals: StatsTotals{Title: Rate{Hit: 50, Total: 100}, Artist: Rate{Hit: 50, Total: 100}}}
	mk := func(title string, guesses, hits int) SongStats {
		// each guess has two chances (title and artist); hits are pooled out of 2*guesses
		return SongStats{Known: true, Title: title, Guesses: guesses, Recognised: Rate{Hit: hits, Total: 2 * guesses}}
	}
	report.Songs = []SongStats{
		mk("Unknown long", 20, 0), mk("Unknown short", 6, 0), mk("Known long", 20, 40), mk("Known short", 6, 12),
		mk("Middling", 10, 10), mk("Too few guesses", 2, 0),
	}
	unknown := songView(report, SongViewUnknown)
	if len(unknown) != 2 || unknown[0].Title != "Unknown long" || unknown[1].Title != "Unknown short" {
		t.Fatalf("least recognised should be below-average songs, longest record first: %+v", unknown)
	}
	known := songView(report, SongViewKnown)
	if len(known) != 2 || known[0].Title != "Known long" || known[1].Title != "Known short" {
		t.Fatalf("most recognised should be above-average songs, longest record first: %+v", known)
	}
}
