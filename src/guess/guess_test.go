package guess

import (
	"context"
	"testing"
)

// Claude said artist=no for these in playtesting even though the artist was
// typed correctly (or nearly); the local matcher's yes must survive that.
func TestHeuristicFloorKeepsObviouslyRightArtist(t *testing.T) {
	cases := []struct {
		name string
		in   Input
	}{
		{"typo in artist", Input{Title: "A Thousand Miles", Artist: "Vanessa Carlton", TitleGuess: "1000 miles", ArtistGuess: "VANESSA CARTLTON"}},
		{"exact artist", Input{Title: "A Thousand Miles", Artist: "Vanessa Carlton", TitleGuess: "1000 MILES", ArtistGuess: "VANESSA CARLTON"}},
		{"apostrophe artist", Input{Title: "Say My Name", Artist: "Destiny's Child", TitleGuess: "say my name", ArtistGuess: "destiny child"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := withHeuristicFloor(context.Background(), c.in, Verdict{TitleCorrect: true, ByAI: true})
			if !got.ArtistCorrect {
				t.Fatalf("artist %q should stay correct for %q, got %+v", c.in.ArtistGuess, c.in.Artist, got)
			}
			if !got.ByAI {
				t.Error("ByAI should be preserved")
			}
		})
	}
}

func TestHeuristicFloorDoesNotInventMatches(t *testing.T) {
	in := Input{Title: "Say My Name", Artist: "Destiny's Child", TitleGuess: "baby", ArtistGuess: "justin bieber"}
	got := withHeuristicFloor(context.Background(), in, Verdict{ByAI: true})
	if got.TitleCorrect || got.ArtistCorrect {
		t.Fatalf("wrong guess must stay wrong, got %+v", got)
	}
}
