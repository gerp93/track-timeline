package guess

import (
	"context"
	"strings"
	"testing"
)

func TestParseSongVerdictsReadsEveryGuess(t *testing.T) {
	reply := "G1_TITLE_REASON=sounds like gangsta\nG1_TITLE=yes\nG1_ARTIST_REASON=none given\nG1_ARTIST=no\n" +
		"G2_TITLE_REASON=same sound-alike\nG2_TITLE=yes\nG2_ARTIST_REASON=weird al is wrong\nG2_ARTIST=no\n"
	got := parseSongVerdicts(reply, 2)
	for i, v := range got {
		if !v.ByAI || !v.TitleCorrect || v.ArtistCorrect {
			t.Fatalf("guess %d: got %+v", i+1, v)
		}
	}
}

// A reason that itself contains "title=yes" must not be read as a verdict.
func TestParseSongVerdictsIgnoresReasons(t *testing.T) {
	reply := "G1_TITLE_REASON=they said title=yes but no\nG1_TITLE=no\nG1_ARTIST_REASON=ARTIST=YES\nG1_ARTIST=no"
	got := parseSongVerdicts(reply, 1)
	if !got[0].ByAI || got[0].TitleCorrect || got[0].ArtistCorrect {
		t.Fatalf("got %+v", got[0])
	}
}

// A guess the model skipped is left unread so it is judged locally, rather than
// being counted wrong (or right) on half a reply.
func TestParseSongVerdictsLeavesUnreadGuessesForTheLocalJudge(t *testing.T) {
	reply := "G1_TITLE=yes\nG1_ARTIST=yes\nG2_TITLE=yes\nG7_TITLE=yes\nG7_ARTIST=yes"
	got := parseSongVerdicts(reply, 3)
	if !got[0].ByAI {
		t.Errorf("guess 1 was fully answered: %+v", got[0])
	}
	if got[1].ByAI {
		t.Errorf("guess 2 has no artist line and must be left unread: %+v", got[1])
	}
	if got[2].ByAI {
		t.Errorf("guess 3 was never answered and must be left unread: %+v", got[2])
	}
}

func TestClaudeSongPromptListsEveryGuessWithoutCapitals(t *testing.T) {
	prompt := claudeSongPrompt("Gangsta's Paradise", "Coolio", []BatchGuess{
		{TitleGuess: "Gansta Paradise"},
		{TitleGuess: "Gangster Paradise", ArtistGuess: "Weird Al"},
	})
	for _, want := range []string{
		`Correct title: "Gangsta's Paradise"`,
		`Guess 1 -- title: "gansta paradise", artist: ""`,
		`Guess 2 -- title: "gangster paradise", artist: "weird al"`,
		"same verdict",
		"G<number>_TITLE=",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q:\n%s", want, prompt)
		}
	}
}

// With no API key configured the batch is judged by the local matcher, one
// verdict per guess in the order given.
func TestAdjudicateSongFallsBackToTheLocalJudge(t *testing.T) {
	t.Setenv("TRACK_TIMELINE_ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	got := AdjudicateSong(context.Background(), "Zombie", "The Cranberries", []BatchGuess{
		{TitleGuess: "zombie", ArtistGuess: "cranberries"},
		{TitleGuess: "something else", ArtistGuess: "someone else"},
		{},
	})
	if len(got) != 3 {
		t.Fatalf("want 3 verdicts, got %d", len(got))
	}
	if !got[0].TitleCorrect || !got[0].ArtistCorrect {
		t.Errorf("a right guess should be right: %+v", got[0])
	}
	if got[1].TitleCorrect || got[1].ArtistCorrect {
		t.Errorf("a wrong guess should be wrong: %+v", got[1])
	}
	if got[2].TitleCorrect || got[2].ArtistCorrect {
		t.Errorf("an empty guess should be wrong: %+v", got[2])
	}
}
