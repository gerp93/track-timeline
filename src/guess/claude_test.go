package guess

import (
	"strings"
	"testing"
)

func TestParseClaudeVerdict(t *testing.T) {
	parsed, err := parseClaudeVerdict("TITLE=yes ARTIST=no", false)
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

	titleOnly, err := parseClaudeVerdict("TITLE=yes", true)
	if err != nil {
		t.Fatal(err)
	}
	titleOnly = finalizeClaudeVerdict(titleOnly, 60)
	if !titleOnly.TitleCorrect || titleOnly.ArtistCorrect || titleOnly.ArtistMatchPercent != 0 {
		t.Fatalf("title-only got %+v", titleOnly)
	}

	if _, err := parseClaudeVerdict("TITLE=yes", false); err == nil {
		t.Fatal("expected an error when artist is missing")
	}
	if _, err := parseClaudeVerdict("nope", false); err == nil {
		t.Fatal("expected an error for an unreadable reply")
	}
	if _, err := parseClaudeVerdict("TITLE=maybe ARTIST=yes", false); err == nil {
		t.Fatal("expected an error for a maybe")
	}
	if _, err := parseClaudeVerdict("TITLE=80 ARTIST=no", false); err == nil {
		t.Fatal("expected an error for a percentage")
	}
}

func TestClaudePromptTitleOnlyOmitsArtistScore(t *testing.T) {
	prompt := claudePrompt(Input{
		Title:     "Heroes",
		Artist:    "David Bowie",
		TitleOnly: true,
	}, "heros", "")
	if strings.Contains(prompt, "ARTIST=yes|no") {
		t.Fatal("title-only prompt should not ask for an artist verdict")
	}
	if !strings.Contains(prompt, "TITLE=yes|no") {
		t.Fatal("title-only prompt should ask for a title verdict")
	}
	if !strings.Contains(prompt, "context only") {
		t.Fatal("title-only prompt should mark the artist as context")
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

func TestClaudeModelMatchesConfig(t *testing.T) {
	if ClaudeModel() == "" {
		t.Fatal("ClaudeModel should expose the configured model id")
	}
	if !strings.Contains(ClaudeModel(), "haiku") {
		t.Fatalf("unexpected model %q", ClaudeModel())
	}
}
