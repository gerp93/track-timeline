package guess

import (
	"context"
	"errors"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// PingClaude makes one minimal real request to the Claude API, with the same
// client and model the judge uses, and returns nil when it answered with some
// text. It exists for the admin API Status page: a request that costs a few
// tokens, not a judgement of any guess.
func PingClaude(ctx context.Context) error {
	judge, ok := defaultClaudeJudge()
	if !ok {
		return errors.New("no Claude API key is configured")
	}

	message, err := judge.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     claudeAPIModel,
		MaxTokens: 8,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("Reply with the single word: ok")),
		},
	})
	if err != nil {
		return err
	}

	var text string
	for _, block := range message.Content {
		if b, ok := block.AsAny().(anthropic.TextBlock); ok {
			text += b.Text
		}
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("Claude answered with an empty reply")
	}
	return nil
}
