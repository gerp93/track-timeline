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
	if !CanBuyCard(3, 3, 5, false) {
		t.Fatal("3 songs with 3 tokens toward 5 should allow buy")
	}
	if CanBuyCard(4, 3, 5, false) {
		t.Fatal("one away from winning must not allow buy")
	}
	if CanBuyCard(3, 2, 5, false) {
		t.Fatal("not enough tokens must not allow buy")
	}
	if CanBuyCard(3, 3, 5, true) {
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

func TestGuessQualifies(t *testing.T) {
	both := Guess{TitleCorrect: true, ArtistCorrect: true}
	titleOnly := Guess{TitleCorrect: true, ArtistCorrect: false}
	artistOnly := Guess{TitleCorrect: false, ArtistCorrect: true}
	neither := Guess{TitleCorrect: false, ArtistCorrect: false}

	cases := []struct {
		mode string
		g    Guess
		want bool
	}{
		{GuessModeBoth, both, true},
		{GuessModeBoth, titleOnly, false},
		{GuessModeBoth, artistOnly, false},
		{GuessModeTitle, both, true},
		{GuessModeTitle, titleOnly, true},
		{GuessModeTitle, artistOnly, false},
		{GuessModeEither, titleOnly, true},
		{GuessModeEither, artistOnly, true},
		{GuessModeEither, neither, false},
		{GuessModeOff, both, false},
	}
	for _, test := range cases {
		if got := GuessQualifies(test.g, test.mode); got != test.want {
			t.Errorf("GuessQualifies(%+v, %q) = %v, want %v", test.g, test.mode, got, test.want)
		}
	}
}

func TestPickGuessTokenWinnerTurnPlayerSupersedes(t *testing.T) {
	first := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	second := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	turn := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	turnId := uuid.NullUUID{UUID: turn, Valid: true}

	// The turn player's qualifying guess wins even though it was submitted
	// last — being on turn supersedes submit order entirely.
	guesses := []Guess{
		{PlayerId: first, PlayerName: "Kaleb", TitleCorrect: true, ArtistCorrect: true, GuessText: "full"},
		{PlayerId: second, PlayerName: "other", TitleCorrect: true, ArtistCorrect: true, GuessText: "also full"},
		{PlayerId: turn, PlayerName: "test2", TitleCorrect: true, ArtistCorrect: false, GuessText: "title only"},
	}
	got, ok := pickGuessTokenWinner(guesses, GuessModeEither, turnId)
	if !ok || got.PlayerId != turn {
		t.Fatalf("turn player's qualifying guess should win regardless of order, got ok=%v %+v", ok, got)
	}

	// GuessModeBoth: the turn player's title-only guess does not qualify, so
	// it falls back to a pure race among the non-turn players -- earliest
	// qualifying submit among them wins.
	got, ok = pickGuessTokenWinner(guesses, GuessModeBoth, turnId)
	if !ok || got.PlayerId != first {
		t.Fatalf("both mode: turn player doesn't qualify, want earliest qualifying non-turn guess, got ok=%v %+v", ok, got)
	}

	// The turn player never guessed this round: falls back to the race among
	// everyone else, in submit order.
	noTurnGuess := []Guess{
		{PlayerId: second, PlayerName: "other", TitleCorrect: true, ArtistCorrect: true, GuessText: "also full"},
		{PlayerId: first, PlayerName: "Kaleb", TitleCorrect: true, ArtistCorrect: true, GuessText: "full"},
	}
	got, ok = pickGuessTokenWinner(noTurnGuess, GuessModeEither, turnId)
	if !ok || got.PlayerId != second {
		t.Fatalf("no turn-player guess: want earliest qualifying non-turn guess, got ok=%v %+v", ok, got)
	}

	// No current turn player at all (invalid uuid): pure race among everyone,
	// submit order only.
	got, ok = pickGuessTokenWinner(noTurnGuess, GuessModeEither, uuid.NullUUID{})
	if !ok || got.PlayerId != second {
		t.Fatalf("no current player: want earliest qualifying guess, got ok=%v %+v", ok, got)
	}
}

func TestValidateGuessMatchPercent(t *testing.T) {
	for _, percent := range []int{60, 70, 80, 90} {
		if err := ValidateGuessMatchPercent(percent); err != nil {
			t.Errorf("ValidateGuessMatchPercent(%d) = %v, want nil", percent, err)
		}
	}
	for _, percent := range []int{50, 100, 0, 85} {
		if err := ValidateGuessMatchPercent(percent); err == nil {
			t.Errorf("ValidateGuessMatchPercent(%d) = nil, want an error", percent)
		}
	}
}
