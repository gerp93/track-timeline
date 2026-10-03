package apiTrackTimeline

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/guess"
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

// TestDescribeVerdictReportsWhatWasEarned guards the guess-token rule: each
// right part earns its own tokens the moment it is judged (see
// database.AwardGuessToken), regardless of turn order or who else guessed, so
// the private message states the amount and never hedges or mentions others.
func TestDescribeVerdictReportsWhatWasEarned(t *testing.T) {
	eco := database.CurrentEconomy()

	both := describeVerdict(guess.Verdict{TitleCorrect: true, ArtistCorrect: true})
	if !strings.Contains(both, "You earned "+eco.Tokens(eco.MaxGuessTokens)+"!") {
		t.Fatalf("a perfect guess should report %s, got %q", eco.Tokens(eco.MaxGuessTokens), both)
	}
	if strings.Contains(both, "holds up") || strings.Contains(both, "other player") || strings.Contains(both, "first in line") {
		t.Errorf("no caveat/hedge/race language is needed, got %q", both)
	}

	for name, verdict := range map[string]guess.Verdict{
		"title only":  {TitleCorrect: true},
		"artist only": {ArtistCorrect: true},
	} {
		if got := describeVerdict(verdict); !strings.Contains(got, "You earned "+eco.Tokens(eco.GuessTokensPerPart)+"!") {
			t.Errorf("%s should report %s, got %q", name, eco.Tokens(eco.GuessTokensPerPart), got)
		}
	}

	// A wrong guess never mentions tokens.
	if got := describeVerdict(guess.Verdict{}); strings.Contains(got, "token") {
		t.Errorf("a wrong guess should not mention tokens at all, got %q", got)
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

// A right part is confirmed back to the guesser, but a wrong part is never
// revealed — a correct title must not leak the artist.
func TestDescribeRightPartsOnlyNamesWhatWasRight(t *testing.T) {
	if got := describeRightParts(true, true, "Africa", "Toto"); got != `It's "Africa" by Toto.` {
		t.Errorf("both right: got %q", got)
	}
	got := describeRightParts(true, false, "Africa", "Toto")
	if !strings.Contains(got, "Africa") || strings.Contains(got, "Toto") {
		t.Errorf("title only should name the title and not the artist, got %q", got)
	}
	got = describeRightParts(false, true, "Africa", "Toto")
	if !strings.Contains(got, "Toto") || strings.Contains(got, "Africa") {
		t.Errorf("artist only should name the artist and not the title, got %q", got)
	}
	if got := describeRightParts(false, false, "Africa", "Toto"); got != "" {
		t.Errorf("a wrong guess should reveal nothing, got %q", got)
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
