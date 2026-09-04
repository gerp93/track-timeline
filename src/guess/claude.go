package guess

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	JudgeLocal  = "local"
	JudgeClaude = "claude"
)

func anthropicAPIKey() string {
	if key := strings.TrimSpace(os.Getenv("TRACK_TIMELINE_ANTHROPIC_API_KEY")); key != "" {
		return key
	}
	return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
}

// ClaudeConfigured is true when a Claude API key is in the environment, so
// lobby setup can offer intent judging for real rather than a fallback.
func ClaudeConfigured() bool {
	return anthropicAPIKey() != ""
}

// ClaudeJudge asks Claude whether the player meant the right title and artist.
// Typos, nicknames, and messy wording are fine; a different song or performer
// is not. Yes/no only — match percents are 100 or 0 to match that boolean.
type ClaudeJudge struct {
	client anthropic.Client
}

var (
	claudeOnce  sync.Once
	claudeJudge ClaudeJudge
	claudeReady bool

	// claudeAPIModel is the Anthropic model id sent on every Claude judge call.
	claudeAPIModel = anthropic.ModelClaudeHaiku4_5
)

// ClaudeModel is the model id the Claude judge is configured to call.
func ClaudeModel() string {
	return string(claudeAPIModel)
}

// ClaudePromptPreview builds the exact user prompt the Claude judge would send
// for this card and guess. Empty said fields show as empty quoted strings.
func ClaudePromptPreview(titleOnly bool, title, artist, titleSaid, artistSaid, combined string) string {
	return claudePrompt(Input{
		Title:     title,
		Artist:    artist,
		Guess:     combined,
		TitleOnly: titleOnly,
	}, titleSaid, artistSaid)
}

func defaultClaudeJudge() (ClaudeJudge, bool) {
	claudeOnce.Do(func() {
		key := anthropicAPIKey()
		if key == "" {
			return
		}
		claudeJudge = ClaudeJudge{client: anthropic.NewClient(option.WithAPIKey(key))}
		claudeReady = true
	})
	return claudeJudge, claudeReady
}

func (j ClaudeJudge) Judge(ctx context.Context, in Input) (Verdict, error) {
	titleSaid := strings.TrimSpace(in.TitleGuess)
	artistSaid := strings.TrimSpace(in.ArtistGuess)
	if titleSaid == "" && artistSaid == "" {
		titleSaid = strings.TrimSpace(in.Guess)
	}

	prompt := claudePrompt(in, titleSaid, artistSaid)

	message, err := j.client.Messages.New(ctx, anthropic.MessageNewParams{
		// Claude Haiku 4.5 — short yes/no classification, not a reasoning model.
		Model:     claudeAPIModel,
		MaxTokens: 32,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return Verdict{}, err
	}

	var text string
	for _, block := range message.Content {
		if b, ok := block.AsAny().(anthropic.TextBlock); ok {
			text += b.Text
		}
	}

	verdict, err := parseClaudeVerdict(text, in.TitleOnly)
	if err != nil {
		return Verdict{}, err
	}
	return finalizeClaudeVerdict(verdict, in.MinMatchPercent), nil
}

func claudePrompt(in Input, titleSaid, artistSaid string) string {
	intent := "Judge intent, not spelling. Accept typos, wrong word order, missing punctuation, " +
		"nicknames, abbreviations, partial titles, and phonetic / sound-alike spellings " +
		"(for example \"deaf leopard\" for Def Leppard, \"led zepplin\" for Led Zeppelin) " +
		"if a quizmaster would know what they meant. " +
		"Do not accept a different song or a different performer.\n" +
		"You must call it. Never maybe, never a percentage, never anything but yes or no."

	if in.TitleOnly {
		return fmt.Sprintf(
			"A player is naming a song. Decide if they *meant* the correct title.\n"+
				"The artist is shown only as context; do not score it.\n\n"+
				"Correct title: %q\nCorrect artist (context only): %q\n"+
				"What they typed: %q\n\n"+
				"%s\n"+
				"An empty guess is TITLE=no.\n\n"+
				"Reply with exactly one line and nothing else:\n"+
				"TITLE=yes|no",
			in.Title, in.Artist, titleSaid, intent,
		)
	}

	return fmt.Sprintf(
		"A player is naming a song. Decide if they *meant* the correct title and the correct artist.\n\n"+
			"Correct title: %q\nCorrect artist: %q\n"+
			"What they typed for the title: %q\nWhat they typed for the artist: %q\n"+
			"Combined: %q\n\n"+
			"%s\n"+
			"Score title and artist independently. A yes on one does not change the other. "+
			"Do not decide whether they earned a token.\n"+
			"An empty title guess is TITLE=no. An empty artist guess is ARTIST=no.\n\n"+
			"Reply with exactly one line and nothing else:\n"+
			"TITLE=yes|no ARTIST=yes|no",
		in.Title, in.Artist, titleSaid, artistSaid, strings.TrimSpace(in.Guess), intent,
	)
}

func parseYesNo(value string) (bool, error) {
	switch value {
	case "YES":
		return true, nil
	case "NO":
		return false, nil
	default:
		return false, fmt.Errorf("not a yes/no: %q", value)
	}
}

func parseClaudeVerdict(text string, titleOnly bool) (Verdict, error) {
	fields := strings.Fields(strings.ToUpper(strings.TrimSpace(text)))
	var verdict Verdict
	var sawTitle, sawArtist bool

	for _, field := range fields {
		switch {
		case strings.HasPrefix(field, "TITLE="):
			yes, err := parseYesNo(strings.TrimPrefix(field, "TITLE="))
			if err != nil {
				return Verdict{}, err
			}
			verdict.TitleCorrect = yes
			sawTitle = true
		case strings.HasPrefix(field, "ARTIST="):
			if titleOnly {
				continue
			}
			yes, err := parseYesNo(strings.TrimPrefix(field, "ARTIST="))
			if err != nil {
				return Verdict{}, err
			}
			verdict.ArtistCorrect = yes
			sawArtist = true
		}
	}

	if !sawTitle {
		return Verdict{}, errors.New("could not read a verdict from the model reply")
	}
	if titleOnly {
		verdict.ArtistCorrect = false
		return verdict, nil
	}
	if !sawArtist {
		return Verdict{}, errors.New("could not read a verdict from the model reply")
	}
	return verdict, nil
}

// finalizeClaudeVerdict maps the model's call onto the same win bar the local
// judge uses: yes is 100%, no is 0%, and correct means that score meets the
// lobby percent. There is no in-between score from Claude.
func finalizeClaudeVerdict(verdict Verdict, minMatchPercent int) Verdict {
	verdict.TitleMatchPercent = binaryMatchPercent(verdict.TitleCorrect)
	verdict.ArtistMatchPercent = binaryMatchPercent(verdict.ArtistCorrect)
	verdict.TitleCorrect = meetsMatchBar(verdict.TitleMatchPercent, minMatchPercent)
	verdict.ArtistCorrect = meetsMatchBar(verdict.ArtistMatchPercent, minMatchPercent)
	return verdict
}

func binaryMatchPercent(yes bool) float64 {
	if yes {
		return 100
	}
	return 0
}
