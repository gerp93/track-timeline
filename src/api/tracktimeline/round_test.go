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
// them guessed first. It also guards that a non-turn player already beaten
// to a qualifying guess is told that plainly (not hedged as "if this holds
// up") since submit order, once recorded, can never be overtaken.
func TestDescribeVerdictTokenPromiseMatchesWhoCanActuallyWinIt(t *testing.T) {
	qualifying := guess.Verdict{TitleCorrect: true, ArtistCorrect: true}

	turnPlayerMsg := describeVerdict(qualifying, database.GuessModeBoth, true, 0)
	if !strings.Contains(turnPlayerMsg, "You'll get the token at reveal") {
		t.Fatalf("turn player: expected an unconditional token promise, got %q", turnPlayerMsg)
	}
	if strings.Contains(turnPlayerMsg, "doesn't also") || strings.Contains(turnPlayerMsg, "holds up") {
		t.Errorf("turn player's own qualifying guess always wins the token -- no caveat/hedge needed, got %q", turnPlayerMsg)
	}

	firstInLineMsg := describeVerdict(qualifying, database.GuessModeBoth, false, 0)
	if !strings.Contains(firstInLineMsg, "you'll get it at reveal") {
		t.Fatalf("first-in-line non-turn player: expected a (conditional) token mention, got %q", firstInLineMsg)
	}
	if !strings.Contains(firstInLineMsg, "current player") {
		t.Errorf("first-in-line non-turn player's promise must be conditioned on the current player not also getting it right, got %q", firstInLineMsg)
	}
	if strings.Contains(firstInLineMsg, "holds up") {
		t.Errorf("verdict is already decided at submit time -- must not hedge with \"holds up\", got %q", firstInLineMsg)
	}

	beatenMsg := describeVerdict(qualifying, database.GuessModeBoth, false, 2)
	if strings.Contains(beatenMsg, "token at reveal") || strings.Contains(beatenMsg, "you'll get") {
		t.Errorf("a non-turn player already beaten to it should be told plainly they lost the token, not offered a promise, got %q", beatenMsg)
	}
	if !strings.Contains(beatenMsg, "2 other players") {
		t.Errorf("expected the beaten guesser to be told how many players beat them, got %q", beatenMsg)
	}
	if !strings.Contains(beatenMsg, "won't get the token") {
		t.Errorf("expected a definitive (not hedged) statement that they lost the token, got %q", beatenMsg)
	}

	// A non-qualifying guess never mentions the token either way.
	notQualifying := guess.Verdict{TitleCorrect: false, ArtistCorrect: false}
	if got := describeVerdict(notQualifying, database.GuessModeBoth, false, 0); strings.Contains(got, "token") {
		t.Errorf("a non-qualifying guess should not mention the token at all, got %q", got)
	}
}
