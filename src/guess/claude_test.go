package guess

import (
	"strings"
	"testing"
)

func TestParseClaudeVerdict(t *testing.T) {
	parsed, err := parseClaudeVerdict("TITLE=yes ARTIST=no")
	if err != nil {
		t.Fatal(err)
	}
	verdict := finalizeClaudeVerdict(parsed, 80)
	if !verdict.TitleCorrect || verdict.ArtistCorrect {
		t.Fatalf("got %+v", verdict)
	}
	if verdict.TitleMatchPercent != 100 || verdict.ArtistMatchPercent != 0 {
		t.Fatalf("percents %+v", verdict)
	}

	if _, err := parseClaudeVerdict("TITLE=yes"); err == nil {
		t.Fatal("expected an error when artist is missing")
	}
	if _, err := parseClaudeVerdict("nope"); err == nil {
		t.Fatal("expected an error for an unreadable reply")
	}
	if _, err := parseClaudeVerdict("TITLE=maybe ARTIST=yes"); err == nil {
		t.Fatal("expected an error for a maybe")
	}
	if _, err := parseClaudeVerdict("TITLE=80 ARTIST=no"); err == nil {
		t.Fatal("expected an error for a percentage")
	}
}

// The prompt asks for a reason before each verdict (see replyFormat). A reason
// that happens to mention a verdict-shaped token must not be read as the verdict.
func TestParseClaudeVerdictWithReasons(t *testing.T) {
	reply := "TITLE_REASON=Numeral for thousand, clear intent\n" +
		"TITLE=yes\n" +
		"ARTIST_REASON=Says ARTIST=yes but wrong performer\n" +
		"ARTIST=no"
	parsed, err := parseClaudeVerdict(reply)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.TitleCorrect || parsed.ArtistCorrect {
		t.Fatalf("got %+v", parsed)
	}

	reply = "TITLE_REASON=Wrong song\nTITLE=no\nARTIST_REASON=Missing apostrophe is fine\nARTIST=yes"
	parsed, err = parseClaudeVerdict(reply)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.TitleCorrect || !parsed.ArtistCorrect {
		t.Fatalf("got %+v", parsed)
	}
}

func TestClaudePromptAcceptsPhoneticSpellings(t *testing.T) {
	prompt := claudePrompt(Input{
		Title:  "Pour Some Sugar On Me",
		Artist: "Def Leppard",
	}, "Pour some sugar", "deaf leopard")
	lower := strings.ToLower(prompt)
	if !strings.Contains(lower, "phonetic") || !strings.Contains(lower, "deaf leopard") {
		t.Fatal("prompt should call out phonetic / sound-alike spellings")
	}
}

// TestClaudePromptAllowsMissingFeaturedArtist guards the leniency rule that
// naming only the main artist is correct even when the authored credit
// includes a featured artist -- matching the local judge's stripFeaturing
// behavior (see normalized.go).
func TestClaudePromptAllowsMissingFeaturedArtist(t *testing.T) {
	prompt := claudePrompt(Input{
		Title:  "Runaway",
		Artist: "Kanye West feat. Pusha T",
	}, "Runaway", "Kanye West")
	lower := strings.ToLower(prompt)
	if !strings.Contains(lower, "featured artist") {
		t.Fatal("prompt should call out leniency for a missing featured artist")
	}
}

func TestClaudeModelMatchesConfig(t *testing.T) {
	if ClaudeModel() == "" {
		t.Fatal("ClaudeModel should expose the configured model id")
	}
	if !strings.Contains(ClaudeModel(), "haiku") {
		t.Fatalf("unexpected model %q", ClaudeModel())
	}
}
