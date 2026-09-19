package database

import (
	"testing"

	"github.com/google/uuid"
)

// timelineOf builds a timeline from years alone; nothing in IsPlacementCorrect
// reads any other field.
func timelineOf(years ...int) []TimelineCard {
	cards := make([]TimelineCard, 0, len(years))
	for _, year := range years {
		cards = append(cards, TimelineCard{ReleaseYear: year})
	}
	return cards
}

func TestIsPlacementCorrect(t *testing.T) {
	tests := []struct {
		name     string
		timeline []TimelineCard
		position int
		year     int
		want     bool
	}{
		// A player's first card is dealt, so the smallest real timeline a
		// placement is judged against has one card in it.
		{"only slot in a one-card timeline, before", timelineOf(1980), 0, 1975, true},
		{"only slot in a one-card timeline, after", timelineOf(1980), 1, 1985, true},
		{"before a later card but placed after it", timelineOf(1980), 1, 1975, false},
		{"after an earlier card but placed before it", timelineOf(1980), 0, 1985, false},

		{"between two cards, correct", timelineOf(1970, 1990), 1, 1980, true},
		{"between two cards, too early", timelineOf(1970, 1990), 1, 1965, false},
		{"between two cards, too late", timelineOf(1970, 1990), 1, 1995, false},
		{"at the very start, correct", timelineOf(1970, 1990), 0, 1960, true},
		{"at the very end, correct", timelineOf(1970, 1990), 2, 1999, true},
		{"at the very start, wrong", timelineOf(1970, 1990), 0, 1980, false},
		{"at the very end, wrong", timelineOf(1970, 1990), 2, 1980, false},

		// Two songs from the same year are genuinely in order either way
		// round, so both sides of a tie have to count.
		{"tie with the earlier neighbour", timelineOf(1980, 1990), 1, 1980, true},
		{"tie with the later neighbour", timelineOf(1980, 1990), 1, 1990, true},
		{"tie placed before an identical year", timelineOf(1980), 0, 1980, true},
		{"tie placed after an identical year", timelineOf(1980), 1, 1980, true},

		// A position outside the timeline is not a wrong guess, it is a
		// malformed request; treating it as incorrect keeps the caller simple.
		{"position below zero", timelineOf(1980), -1, 1975, false},
		{"position past the end", timelineOf(1980), 2, 1985, false},

		{"empty timeline accepts position zero", timelineOf(), 0, 1980, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := IsPlacementCorrect(test.timeline, test.position, test.year)
			if got != test.want {
				t.Errorf("IsPlacementCorrect(%v, %d, %d) = %v, want %v",
					test.timeline, test.position, test.year, got, test.want)
			}
		})
	}
}

func TestPlacementYearRangeContains(t *testing.T) {
	// When Doves Cry (1984): after 1978 and before 1995 both contain it —
	// both valid → steal denied (AttemptSteal checks original.Contains(year)).
	after1978 := PlacementYearRangeOf(timelineOf(1978), 1)
	before1995 := PlacementYearRangeOf(timelineOf(1995), 0)
	if !after1978.Contains(1984) {
		t.Fatal("after 1978 should contain 1984")
	}
	if !before1995.Contains(1984) {
		t.Fatal("before 1995 should contain 1984")
	}

	// Killer Queen (1974): both before-N slots also contain it.
	before1984 := PlacementYearRangeOf(timelineOf(1984), 0)
	before1978 := PlacementYearRangeOf(timelineOf(1978), 0)
	if !before1984.Contains(1974) || !before1978.Contains(1974) {
		t.Fatal("both before-slots should contain 1974")
	}

	// Original wrong: after 1990 does not contain 1984 — a correct stealer wins.
	after1990 := PlacementYearRangeOf(timelineOf(1990), 1)
	if after1990.Contains(1984) {
		t.Fatal("after 1990 must not contain 1984")
	}

	between := PlacementYearRangeOf(timelineOf(1971, 1989), 1)
	if !between.Contains(1971) || !between.Contains(1989) {
		t.Fatal("equal neighbour years must count as in-range")
	}
	if between.Contains(1970) || between.Contains(1990) {
		t.Fatal("years outside the neighbours must be out of range")
	}

	empty := PlacementYearRangeOf(timelineOf(), 0)
	if !empty.Contains(1984) {
		t.Fatal("any-year slot should contain every year")
	}
}

func TestPlacementYearRangeFormat(t *testing.T) {
	tests := []struct {
		r    PlacementYearRange
		want string
	}{
		{PlacementYearRangeOf(timelineOf(), 0), "any year"},
		{PlacementYearRangeOf(timelineOf(1970), 0), "before 1970"},
		{PlacementYearRangeOf(timelineOf(1989), 1), "after 1989"},
		{PlacementYearRangeOf(timelineOf(1971, 1989), 1), "1971–1989"},
	}

	for _, test := range tests {
		if got := test.r.Format(); got != test.want {
			t.Errorf("Format(%+v) = %q, want %q", test.r, got, test.want)
		}
	}
}

func TestCanBuyCard(t *testing.T) {
	if !CanBuyCard(3, BuyCardCost, 5, false) {
		t.Fatal("3 songs with exactly the buy price toward 5 should allow buy")
	}
	if CanBuyCard(4, BuyCardCost, 5, false) {
		t.Fatal("one away from winning must not allow buy")
	}
	if CanBuyCard(3, BuyCardCost-1, 5, false) {
		t.Fatal("not enough tokens must not allow buy")
	}
	if CanBuyCard(3, BuyCardCost, 5, true) {
		t.Fatal("a strict leader must not be allowed to buy")
	}
}

func TestValidateCardsToWin(t *testing.T) {
	tests := []struct {
		name       string
		cardsToWin int
		totalCards int
		wantErr    bool
	}{
		{"below the minimum", MinCardsToWin - 1, 1000, true},
		{"at the minimum with a big pile", MinCardsToWin, 1000, false},
		{"above the maximum", MaxCardsToWin + 1, 1000, true},
		{"at the maximum with a big pile", MaxCardsToWin, 1000, false},
		{"pile exactly the required ratio", 10, 10 * MinCardsPerWinRatio, false},
		{"pile one short of the ratio", 10, 10*MinCardsPerWinRatio - 1, true},
		{"empty pile", 10, 0, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateCardsToWin(test.cardsToWin, test.totalCards)
			if (err != nil) != test.wantErr {
				t.Errorf("ValidateCardsToWin(%d, %d) error = %v, wantErr %v",
					test.cardsToWin, test.totalCards, err, test.wantErr)
			}
		})
	}
}

func TestValidateStartingTokens(t *testing.T) {
	for _, tokens := range []int{MinStartingTokens, 1, MaxStartingTokens} {
		if err := ValidateStartingTokens(tokens); err != nil {
			t.Errorf("ValidateStartingTokens(%d) = %v, want nil", tokens, err)
		}
	}
	for _, tokens := range []int{MinStartingTokens - 1, MaxStartingTokens + 1} {
		if err := ValidateStartingTokens(tokens); err == nil {
			t.Errorf("ValidateStartingTokens(%d) = nil, want an error", tokens)
		}
	}
}

func TestSameUUIDOrder(t *testing.T) {
	a := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	if !sameUUIDOrder(a, a) {
		t.Error("sameUUIDOrder(a, a) = false, want true")
	}

	reversed := []uuid.UUID{a[2], a[1], a[0]}
	if sameUUIDOrder(a, reversed) {
		t.Error("sameUUIDOrder(a, reversed) = true, want false")
	}

	if sameUUIDOrder(a, a[:2]) {
		t.Error("sameUUIDOrder of different lengths = true, want false")
	}
}

// A guess pays for each part on its own: the right title, the right artist, or
// both, and nothing for neither.
func TestGuessTokensEarned(t *testing.T) {
	cases := []struct {
		name string
		g    Guess
		want int
	}{
		{"both right", Guess{TitleCorrect: true, ArtistCorrect: true}, 2 * GuessTokensPerPart},
		{"title only", Guess{TitleCorrect: true}, GuessTokensPerPart},
		{"artist only", Guess{ArtistCorrect: true}, GuessTokensPerPart},
		{"neither", Guess{}, 0},
	}
	for _, test := range cases {
		if got := GuessTokensEarned(test.g); got != test.want {
			t.Errorf("%s: GuessTokensEarned = %d, want %d", test.name, got, test.want)
		}
	}
}

// One perfect guess pays for one skip, replay or steal, and the buy price is a
// whole number of those: the ratio the economy is balanced around.
func TestEconomyIsInternallyConsistent(t *testing.T) {
	e := CurrentEconomy()
	if e.MaxGuessTokens != 2*e.GuessTokensPerPart {
		t.Errorf("MaxGuessTokens %d should be title + artist = %d", e.MaxGuessTokens, 2*e.GuessTokensPerPart)
	}
	for name, cost := range map[string]int{"skip": e.SkipCost, "replay": e.ReplayCost, "steal": e.StealCost} {
		if cost != e.MaxGuessTokens {
			t.Errorf("%s costs %d, want one perfect guess (%d)", name, cost, e.MaxGuessTokens)
		}
	}
	if e.BuyCardCost%e.MaxGuessTokens != 0 {
		t.Errorf("buy price %d should be a whole number of perfect guesses (%d each)", e.BuyCardCost, e.MaxGuessTokens)
	}
	if e.DefaultStartingTokens < e.MinStartingTokens || e.DefaultStartingTokens > e.MaxStartingTokens {
		t.Errorf("default starting tokens %d is outside %d-%d", e.DefaultStartingTokens, e.MinStartingTokens, e.MaxStartingTokens)
	}
	if got := e.Tokens(1); got != "1 token" {
		t.Errorf("Tokens(1) = %q", got)
	}
	if got := e.Tokens(2); got != "2 tokens" {
		t.Errorf("Tokens(2) = %q", got)
	}
}

// A guess that earned nothing pays nothing, decided before any database call.
func TestAwardGuessTokenPaysNothingForAWrongGuess(t *testing.T) {
	paid, err := AwardGuessToken(uuid.Nil, uuid.Nil, Guess{})
	if err != nil || paid != 0 {
		t.Errorf("a wrong guess: paid=%d err=%v, want no payout", paid, err)
	}
}

// With "never-played songs first" on, every unseen row is dealt before any seen
// row, nothing is dropped, and a normal game is left fully random.
func TestOrderDrawPileFreshFirst(t *testing.T) {
	const total, seenCount = 40, 25
	build := func() ([]uuid.UUID, map[uuid.UUID]bool) {
		ids := make([]uuid.UUID, total)
		seen := make(map[uuid.UUID]bool)
		for i := range ids {
			ids[i] = uuid.New()
			seen[ids[i]] = i < seenCount
		}
		return ids, seen
	}

	for run := 0; run < 20; run++ {
		ids, seen := build()
		ordered := orderDrawPile(ids, seen, true)
		if len(ordered) != total {
			t.Fatalf("ordered %d rows, want %d", len(ordered), total)
		}
		reachedSeen := false
		present := make(map[uuid.UUID]bool)
		for _, id := range ordered {
			present[id] = true
			if seen[id] {
				reachedSeen = true
			} else if reachedSeen {
				t.Fatalf("a never-played song was dealt after a played one (run %d)", run)
			}
		}
		if len(present) != total {
			t.Fatalf("rows were dropped or duplicated: %d distinct of %d", len(present), total)
		}
	}

	// Off: still a permutation of everything, and mixed rather than grouped.
	interleaved := false
	for run := 0; run < 20 && !interleaved; run++ {
		ids, seen := build()
		ordered := orderDrawPile(ids, seen, false)
		if len(ordered) != total {
			t.Fatalf("ordered %d rows, want %d", len(ordered), total)
		}
		reachedSeen := false
		for _, id := range ordered {
			if seen[id] {
				reachedSeen = true
			} else if reachedSeen {
				interleaved = true
			}
		}
	}
	if !interleaved {
		t.Error("with the setting off, the pile should stay fully random, not grouped by played/unplayed")
	}
}
