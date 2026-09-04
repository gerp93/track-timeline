package guess

import (
	"context"
	"testing"
)

func TestNormalizedJudge(t *testing.T) {
	tests := []struct {
		name       string
		guess      string
		title      string
		artist     string
		wantTitle  bool
		wantArtist bool
	}{
		{
			name:  "both named plainly",
			guess: "Heroes by David Bowie", title: "Heroes", artist: "David Bowie",
			wantTitle: true, wantArtist: true,
		},
		{
			name:  "artist only",
			guess: "david bowie", title: "Heroes", artist: "David Bowie",
			wantTitle: false, wantArtist: true,
		},
		{
			name:  "title only",
			guess: "heroes", title: "Heroes", artist: "David Bowie",
			wantTitle: true, wantArtist: false,
		},
		{
			name:  "surname alone is enough for the artist",
			guess: "bowie", title: "Heroes", artist: "David Bowie",
			wantTitle: false, wantArtist: true,
		},
		{
			name:  "small typo in the title still counts",
			guess: "herose", title: "Heroes", artist: "David Bowie",
			wantTitle: true, wantArtist: false,
		},
		{
			name:  "word order does not matter",
			guess: "bowie heroes", title: "Heroes", artist: "David Bowie",
			wantTitle: true, wantArtist: true,
		},
		{
			name:  "remaster suffix on the authored title is ignored",
			guess: "come together", title: "Come Together (Remastered 2009)", artist: "The Beatles",
			wantTitle: true, wantArtist: false,
		},
		{
			name:  "leading 'the' is optional on the artist",
			guess: "beatles", title: "Come Together", artist: "The Beatles",
			wantTitle: false, wantArtist: true,
		},
		{
			name:  "featured artist is not required",
			guess: "kanye west", title: "Runaway", artist: "Kanye West feat. Pusha T",
			wantTitle: false, wantArtist: true,
		},
		{
			name:  "accents are folded",
			guess: "bjork", title: "Army of Me", artist: "Björk",
			wantTitle: false, wantArtist: true,
		},
		{
			name:  "punctuation and apostrophes are ignored",
			guess: "dont stop me now", title: "Don't Stop Me Now", artist: "Queen",
			wantTitle: true, wantArtist: false,
		},
		{
			name:  "partial long title is accepted",
			guess: "sgt peppers lonely hearts club band", title: "Sgt. Pepper's Lonely Hearts Club Band", artist: "The Beatles",
			wantTitle: true, wantArtist: false,
		},
		{
			name:  "a wrong guess scores nothing",
			guess: "purple rain by prince", title: "Heroes", artist: "David Bowie",
			wantTitle: false, wantArtist: false,
		},
		{
			name:  "an empty guess scores nothing",
			guess: "", title: "Heroes", artist: "David Bowie",
			wantTitle: false, wantArtist: false,
		},
		{
			name:  "a near-miss word is not a match",
			guess: "hero", title: "Heroes", artist: "David Bowie",
			wantTitle: false, wantArtist: false,
		},
		{
			name:  "naming a different artist entirely",
			guess: "heroes by queen", title: "Heroes", artist: "David Bowie",
			wantTitle: true, wantArtist: false,
		},
	}

	var judge Normalized
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verdict, err := judge.Judge(context.Background(), Input{
				Guess:  test.guess,
				Title:  test.title,
				Artist: test.artist,
			})
			if err != nil {
				t.Fatalf("Judge returned an error: %v", err)
			}
			if verdict.TitleCorrect != test.wantTitle {
				t.Errorf("TitleCorrect = %v, want %v (guess %q vs title %q)",
					verdict.TitleCorrect, test.wantTitle, test.guess, test.title)
			}
			if verdict.ArtistCorrect != test.wantArtist {
				t.Errorf("ArtistCorrect = %v, want %v (guess %q vs artist %q)",
					verdict.ArtistCorrect, test.wantArtist, test.guess, test.artist)
			}
		})
	}
}

func TestSplitFieldsTitleTypoIsNot100(t *testing.T) {
	var judge Normalized
	verdict, err := judge.Judge(context.Background(), Input{
		TitleGuess:  "killer quee",
		ArtistGuess: "queen",
		Title:       "Killer Queen",
		Artist:      "Queen",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.TitleCorrect || !verdict.ArtistCorrect {
		t.Fatalf("expected a qualifying guess, got %+v", verdict)
	}
	if verdict.TitleMatchPercent >= 100 {
		t.Errorf("title typo reported as %.0f%%, want under 100", verdict.TitleMatchPercent)
	}
	if verdict.ArtistMatchPercent != 100 {
		t.Errorf("artist MatchPercent = %.0f, want 100", verdict.ArtistMatchPercent)
	}
}

func TestHyphenatedArtistMatchesWithoutDash(t *testing.T) {
	var judge Normalized
	verdict, err := judge.Judge(context.Background(), Input{
		TitleGuess:  "take on me",
		ArtistGuess: "aha",
		Title:       "Take On Me",
		Artist:      "a-ha",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.TitleCorrect {
		t.Fatal("expected take on me to match Take On Me")
	}
	if !verdict.ArtistCorrect {
		t.Fatalf("expected aha to match a-ha, got artist %.0f%%", verdict.ArtistMatchPercent)
	}
}

func TestScrambledTitleOrderIsNotAWin(t *testing.T) {
	var judge Normalized
	verdict, err := judge.Judge(context.Background(), Input{
		TitleGuess:  "take me on",
		ArtistGuess: "a-ha",
		Title:       "Take On Me",
		Artist:      "a-ha",
	})
	if err != nil {
		t.Fatal(err)
	}
	if verdict.TitleCorrect {
		t.Fatal("take me on should not count as Take On Me")
	}
	if verdict.TitleMatchPercent >= 100 {
		t.Errorf("scrambled order reported as %.0f%%, want under 100", verdict.TitleMatchPercent)
	}
}

func TestSplitCompoundTitleMatches(t *testing.T) {
	var judge Normalized
	verdict, err := judge.Judge(context.Background(), Input{
		TitleGuess:  "fire work",
		ArtistGuess: "katy perry",
		Title:       "Firework",
		Artist:      "Katy Perry",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.TitleCorrect {
		t.Fatalf("fire work should match Firework, got %.0f%%", verdict.TitleMatchPercent)
	}
}

func TestSurnameCountsAsFullArtistMatch(t *testing.T) {
	var judge Normalized
	verdict, err := judge.Judge(context.Background(), Input{
		TitleGuess:  "firework",
		ArtistGuess: "perry",
		Title:       "Firework",
		Artist:      "Katy Perry",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.ArtistCorrect {
		t.Fatal("Perry should still count as Katy Perry")
	}
	if verdict.ArtistMatchPercent != 100 {
		t.Errorf("surname-only artist reported as %.0f%%, want 100", verdict.ArtistMatchPercent)
	}
}

func TestMinMatchPercentCoverage(t *testing.T) {
	var judge Normalized
	in := Input{
		Guess:  "alpha bravo charlie",
		Title:  "Alpha Bravo Charlie Delta",
		Artist: "Whoever",
	}
	loose, err := judge.Judge(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !loose.TitleCorrect {
		t.Fatal("3 of 4 title words should pass the default 60% bar")
	}

	in.MinMatchPercent = 90
	strict, err := judge.Judge(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if strict.TitleCorrect {
		t.Fatal("3 of 4 title words should fail a 90% bar")
	}
}

// failingJudge stands in for a Judge whose backing service is unavailable.
type failingJudge struct{}

func (failingJudge) Judge(context.Context, Input) (Verdict, error) {
	return Verdict{}, context.DeadlineExceeded
}

// A Judge that is down must degrade the quality of judging, not block the game.
func TestAdjudicateFallsBackWhenJudgeFails(t *testing.T) {
	original := configured
	t.Cleanup(func() { configured = original })

	SetJudge(failingJudge{})

	verdict := Adjudicate(context.Background(), Input{
		Guess:  "heroes by david bowie",
		Title:  "Heroes",
		Artist: "David Bowie",
	})

	if !verdict.TitleCorrect || !verdict.ArtistCorrect {
		t.Errorf("expected the local fallback to score a plainly correct guess, got %+v", verdict)
	}
}

func TestSetJudgeNilRestoresDefault(t *testing.T) {
	original := configured
	t.Cleanup(func() { configured = original })

	SetJudge(failingJudge{})
	SetJudge(nil)

	if _, ok := configured.(Normalized); !ok {
		t.Errorf("SetJudge(nil) left %T configured, want Normalized", configured)
	}
}

func TestEditDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"abc", "abc", 0},
		// A transposition is one edit, not two -- this is the whole reason
		// this is optimal string alignment rather than plain Levenshtein.
		{"heroes", "herose", 1},
		{"ab", "ba", 1},
		{"kitten", "sitting", 3},
		{"hero", "heroes", 2},
	}
	for _, test := range tests {
		if got := editDistance(test.a, test.b); got != test.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", test.a, test.b, got, test.want)
		}
	}
}
