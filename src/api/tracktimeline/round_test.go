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

// TestDescribeVerdictTokenPromiseIsUnconditional guards the current
// guess-token rule: every qualifying guess earns its own token at reveal
// (see database.AwardGuessTokens), regardless of turn order or who else
// guessed, so the private message never hedges or mentions other players.
func TestDescribeVerdictTokenPromiseIsUnconditional(t *testing.T) {
	qualifying := guess.Verdict{TitleCorrect: true, ArtistCorrect: true}

	msg := describeVerdict(qualifying, database.GuessModeBoth)
	if !strings.Contains(msg, "You'll get a token at reveal") {
		t.Fatalf("expected an unconditional token promise, got %q", msg)
	}
	if strings.Contains(msg, "holds up") || strings.Contains(msg, "other player") || strings.Contains(msg, "first in line") {
		t.Errorf("a qualifying guess always earns its own token -- no caveat/hedge/race language needed, got %q", msg)
	}

	// A non-qualifying guess never mentions the token either way.
	notQualifying := guess.Verdict{TitleCorrect: false, ArtistCorrect: false}
	if got := describeVerdict(notQualifying, database.GuessModeBoth); strings.Contains(got, "token") {
		t.Errorf("a non-qualifying guess should not mention the token at all, got %q", got)
	}
}
