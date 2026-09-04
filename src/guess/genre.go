package guess

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// SuggestGenre asks Claude Haiku to pick exactly one name from genres for the
// song. The returned string is always a member of genres (matched
// case-insensitively); unknown replies are an error.
func SuggestGenre(ctx context.Context, title, artist string, genres []string) (string, error) {
	if len(genres) == 0 {
		return "", errors.New("no genres available")
	}
	j, ok := defaultClaudeJudge()
	if !ok {
		return "", errors.New("Claude API key is not configured")
	}

	prompt := genrePrompt(title, artist, genres)
	message, err := j.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     claudeAPIModel,
		MaxTokens: 64,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return "", err
	}

	var text string
	for _, block := range message.Content {
		if b, ok := block.AsAny().(anthropic.TextBlock); ok {
			text += b.Text
		}
	}
	return matchGenreReply(text, genres)
}

func genrePrompt(title, artist string, genres []string) string {
	var b strings.Builder
	b.WriteString("Pick the single best genre for this song from the list below.\n")
	b.WriteString("Use only the song title and artist. Prefer the closest match when unsure.\n\n")
	fmt.Fprintf(&b, "Title: %q\nArtist: %q\n\nAllowed genres (pick exactly one):\n", title, artist)
	for _, g := range genres {
		fmt.Fprintf(&b, "- %s\n", g)
	}
	b.WriteString("\nReply with exactly one line and nothing else:\n")
	b.WriteString("GENRE=<exact genre name from the list>")
	return b.String()
}

func matchGenreReply(text string, genres []string) (string, error) {
	line := strings.TrimSpace(text)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	upper := strings.ToUpper(line)
	name := line
	if strings.HasPrefix(upper, "GENRE=") {
		name = strings.TrimSpace(line[len("GENRE="):])
	}
	name = strings.Trim(name, "\"'`")
	if name == "" {
		return "", errors.New("could not read a genre from the model reply")
	}

	byLower := make(map[string]string, len(genres))
	for _, g := range genres {
		byLower[strings.ToLower(strings.TrimSpace(g))] = g
	}
	if exact, ok := byLower[strings.ToLower(name)]; ok {
		return exact, nil
	}
	return "", fmt.Errorf("model genre %q is not in the allowed list", name)
}
