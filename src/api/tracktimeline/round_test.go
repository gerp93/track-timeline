package apiTrackTimeline

import (
	"strings"
	"testing"
	"unicode/utf8"
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
