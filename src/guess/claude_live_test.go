package guess

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestLiveClaudeJudge calls the real Anthropic API. It is skipped without a
// key. Run with -v to see the model's raw reply for each case:
//
//	go test ./guess -run TestLiveClaudeJudge -v
//
// The first four are guesses that were judged "artist wrong" in playtesting
// despite naming the artist (the bare one-line reply format made the model
// answer ARTIST=no regardless -- see replyFormat). The last two are controls so
// a fix cannot pass by saying yes to everything.
func TestLiveClaudeJudge(t *testing.T) {
	judge, ok := defaultClaudeJudge()
	if !ok {
		t.Skip("no Anthropic API key in the environment")
	}

	cases := []struct {
		name                     string
		in                       Input
		wantTitle, wantArtist    bool
	}{
		{"exact artist", Input{Title: "A Thousand Miles", Artist: "Vanessa Carlton", TitleGuess: "1000 MILES", ArtistGuess: "VANESSA CARLTON"}, true, true},
		{"artist typo", Input{Title: "A Thousand Miles", Artist: "Vanessa Carlton", TitleGuess: "1000 miles", ArtistGuess: "VANESSA CARTLTON"}, true, true},
		{"apostrophe in artist", Input{Title: "Say My Name", Artist: "Destiny's Child", TitleGuess: "say my name", ArtistGuess: "destiny child"}, true, true},
		{"one-letter typo", Input{Title: "She's Country", Artist: "Jason Aldean", TitleGuess: "she's country", ArtistGuess: "jason aldein"}, true, true},
		{"control: wrong artist", Input{Title: "Say My Name", Artist: "Destiny's Child", TitleGuess: "say my name", ArtistGuess: "justin bieber"}, true, false},
		{"control: wrong title", Input{Title: "Say My Name", Artist: "Destiny's Child", TitleGuess: "baby", ArtistGuess: "destiny child"}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.in.Guess = c.in.TitleGuess + " by " + c.in.ArtistGuess
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			verdict, err := judge.Judge(ctx, c.in)
			if err != nil {
				if strings.Contains(err.Error(), "401") {
					t.Skipf("API key rejected: %v", err)
				}
				t.Fatal(err)
			}
			t.Logf("title %q artist %q -> %s", c.in.TitleGuess, c.in.ArtistGuess, strings.ReplaceAll(verdict.Raw, "\n", " | "))
			if verdict.TitleCorrect != c.wantTitle {
				t.Errorf("title judged %v, want %v", verdict.TitleCorrect, c.wantTitle)
			}
			if verdict.ArtistCorrect != c.wantArtist {
				t.Errorf("artist judged %v, want %v", verdict.ArtistCorrect, c.wantArtist)
			}
		})
	}
}

// TestLivePingClaude makes one minimal real request to the Claude API, the same
// call the admin API Status page makes. Skipped without a key.
func TestLivePingClaude(t *testing.T) {
	if _, ok := defaultClaudeJudge(); !ok {
		t.Skip("no Anthropic API key in the environment")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	err := PingClaude(ctx)
	t.Logf("Claude ping took %v, err=%v", time.Since(start), err)
	if err != nil {
		if strings.Contains(err.Error(), "401") {
			t.Skipf("API key rejected: %v", err)
		}
		t.Errorf("the Claude API did not answer a ping: %v", err)
	}
}
