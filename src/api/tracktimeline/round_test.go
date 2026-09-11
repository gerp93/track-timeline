package apiTrackTimeline

import (
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

// TestDescribeVerdictTokenPromiseMatchesWhoCanActuallyWinIt guards the
// private per-guesser message against the guess-token rule it has to stay
// consistent with: the turn player's own qualifying guess always wins the
// token (see database.AwardGuessToken / pickGuessTokenWinner), so telling a
// NON-turn player an unconditional "you'll get the token at reveal" is
// false the moment the turn player also nails it -- regardless of which of
// them guessed first.
func TestDescribeVerdictTokenPromiseMatchesWhoCanActuallyWinIt(t *testing.T) {
	qualifying := guess.Verdict{TitleCorrect: true, ArtistCorrect: true}

	turnPlayerMsg := describeVerdict(qualifying, database.GuessModeBoth, true)
	if !strings.Contains(turnPlayerMsg, "you'll get the token at reveal") {
		t.Fatalf("turn player: expected an unconditional token promise, got %q", turnPlayerMsg)
	}
	if strings.Contains(turnPlayerMsg, "doesn't also") {
		t.Errorf("turn player's own qualifying guess always wins the token -- no caveat needed, got %q", turnPlayerMsg)
	}

	nonTurnPlayerMsg := describeVerdict(qualifying, database.GuessModeBoth, false)
	if !strings.Contains(nonTurnPlayerMsg, "you'll get the token at reveal") {
		t.Fatalf("non-turn player: expected a (conditional) token mention, got %q", nonTurnPlayerMsg)
	}
	if !strings.Contains(nonTurnPlayerMsg, "current player") {
		t.Errorf("non-turn player's promise must be conditioned on the current player not also getting it right, got %q", nonTurnPlayerMsg)
	}

	// A non-qualifying guess never mentions the token either way.
	notQualifying := guess.Verdict{TitleCorrect: false, ArtistCorrect: false}
	if got := describeVerdict(notQualifying, database.GuessModeBoth, false); strings.Contains(got, "token") {
		t.Errorf("a non-qualifying guess should not mention the token at all, got %q", got)
	}
}
