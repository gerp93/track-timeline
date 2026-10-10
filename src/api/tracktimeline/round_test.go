package apiTrackTimeline

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gerp93/track-timeline/database"
)

// TestTruncateRunesDoesNotSplitMultiByteCharacters guards the playtest fix
// for guess text (and, by the same pattern, the genre-assign error log)
// showing up garbled: s[:n] on a raw Go string slices by byte, and a
// multi-byte character (an accented letter, an emoji) sitting across that
// boundary gets cut in half, leaving invalid UTF-8 that renders as mangled
// trailing characters once stored and redisplayed.
func TestTruncateRunesDoesNotSplitMultiByteCharacters(t *testing.T) {
	// "ö" (2 bytes) sits exactly on the old byte-500 boundary.
	s := strings.Repeat("a", 499) + "ö" + strings.Repeat("b", 50)

	got := truncateRunes(s, 500)

	if !utf8.ValidString(got) {
		t.Fatalf("truncateRunes produced invalid UTF-8: %q", got)
	}
	if utf8.RuneCountInString(got) != 500 {
		t.Errorf("expected exactly 500 runes, got %d", utf8.RuneCountInString(got))
	}
	if !strings.HasSuffix(got, "ö") {
		t.Errorf("expected the truncation to keep the full final character \"ö\", got %q", got[len(got)-3:])
	}

	// Shorter than the cap: unchanged.
	if got := truncateRunes("Björk", 500); got != "Björk" {
		t.Errorf("expected a short string to pass through unchanged, got %q", got)
	}
}

// The reveal line for a judged guess says who was right about what, and an AI
// verdict carries no match percent -- Claude's call is yes or no, not a score.
func TestDescribeGuessPublic(t *testing.T) {
	ai := describeGuessPublic(database.Guess{Judged: true, JudgedByAI: true, TitleCorrect: true})
	if ai != "title right, artist wrong — judged by the AI Quizmaster" {
		t.Errorf("AI verdict: got %q", ai)
	}

	local := describeGuessPublic(database.Guess{
		Judged: true, TitleCorrect: true, ArtistCorrect: true, TitleMatchPercent: 100, ArtistMatchPercent: 80,
	})
	if local != "title right (100% match), artist right (80% match)" {
		t.Errorf("local verdict: got %q", local)
	}

	// A guess the judge never got to must not read as a wrong answer.
	if got := describeGuessPublic(database.Guess{}); got != "couldn't be judged" {
		t.Errorf("unjudged guess: got %q", got)
	}
}

// The chat line for a settled wager has to state the stake, not just the
// delta, so the table can see how much was riding on it.
func TestWagerResultNamesStakeAndOutcome(t *testing.T) {
	if got := wagerResult(3, false); got != "wagered 3 tokens and lost 3 tokens" {
		t.Errorf("lost wager: got %q", got)
	}
	if got := wagerResult(1, true); got != "wagered 1 token and won 1 token" {
		t.Errorf("won wager: got %q", got)
	}
}

// A hand-built POST skips the form's maxlength, and both boxes are sent on to
// the AI judge, so the server has to cap them itself.
func TestGuessFieldsCapsEachBox(t *testing.T) {
	form := url.Values{
		"guessTitle":  {strings.Repeat("t", 5000)},
		"guessArtist": {strings.Repeat("a", 5000)},
	}
	req := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	title, artist, _ := guessFields(req)
	if n := utf8.RuneCountInString(title); n != 250 {
		t.Errorf("title is %d runes, want 250", n)
	}
	if n := utf8.RuneCountInString(artist); n != 250 {
		t.Errorf("artist is %d runes, want 250", n)
	}
}

// Buying, stealing, skipping and replaying are choices to pay, so chat says
// "spent"; only a lost wager reads as "lost".
func TestTokensSpentReadsAsAPurchase(t *testing.T) {
	if got := tokensSpent(5); got != "spent 5 tokens" {
		t.Errorf("tokensSpent(5) = %q", got)
	}
	if got := tokensSpent(1); got != "spent 1 token" {
		t.Errorf("tokensSpent(1) = %q", got)
	}
	if got := tokensWonLost(-3); got != "lost 3 tokens" {
		t.Errorf("a lost wager should still read as lost, got %q", got)
	}
}
