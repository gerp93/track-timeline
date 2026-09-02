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
