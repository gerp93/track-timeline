package guess

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// batchJudgeTimeout bounds one song's batch. Longer than judgeTimeout because
// one reply carries a verdict for every guess made about the song, not one.
const batchJudgeTimeout = 10 * time.Second

// BatchGuess is one player's guess about a song, as typed.
type BatchGuess struct {
	TitleGuess  string
	ArtistGuess string
	// Guess is the combined "title by artist" text, used only when both split
	// fields are empty.
	Guess string
}

func (g BatchGuess) input(title, artist string) Input {
	return Input{
		Guess:       g.Guess,
		TitleGuess:  strings.TrimSpace(g.TitleGuess),
		ArtistGuess: strings.TrimSpace(g.ArtistGuess),
		Title:       title,
		Artist:      artist,
	}
}

// AdjudicateSong judges every guess made about one song in a single Claude
// request, so the model sees everything the table said and holds all of it to
// one standard: "gansta paradise" and "gangster paradise" are the same attempt
// and must not be judged differently just because they arrived separately.
//
// The result has one Verdict per guess, in the same order. Like AdjudicateGuess
// it never fails: a missing key, an API error, a timeout, or a reply that leaves
// a guess unread all fall back to the local word matcher for the affected
// guesses, and a Claude verdict the local matcher can plainly see is right is
// never overruled (withHeuristicFloor).
func AdjudicateSong(ctx context.Context, title, artist string, guesses []BatchGuess) []Verdict {
	verdicts := make([]Verdict, len(guesses))
	if len(guesses) == 0 {
		return verdicts
	}

	var fromClaude []Verdict
	if claude, ok := defaultClaudeJudge(); ok {
		timed, cancel := context.WithTimeout(ctx, batchJudgeTimeout)
		defer cancel()
		parsed, err := claude.judgeSong(timed, title, artist, guesses)
		if err != nil {
			log.Printf("guess: batch judge failed (%v); falling back to local matching", err)
		} else {
			fromClaude = parsed
		}
	}

	for i, g := range guesses {
		in := g.input(title, artist)
		if fromClaude != nil && i < len(fromClaude) && fromClaude[i].ByAI {
			verdicts[i] = withHeuristicFloor(ctx, in, fromClaude[i])
			continue
		}
		verdicts[i] = runJudge(ctx, Normalized{}, in)
	}
	return verdicts
}

// judgeSong is the one Claude call. It returns a slice the same length as
// guesses; an entry that could not be read is left with ByAI false so the caller
// judges just that guess locally.
func (j ClaudeJudge) judgeSong(ctx context.Context, title, artist string, guesses []BatchGuess) ([]Verdict, error) {
	prompt := claudeSongPrompt(title, artist, guesses)

	message, err := j.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model: claudeAPIModel,
		// A short reason and a verdict for each of title and artist, per guess.
		MaxTokens:   int64(120*len(guesses) + 100),
		Temperature: anthropic.Float(0),
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return nil, err
	}

	var text string
	for _, block := range message.Content {
		if b, ok := block.AsAny().(anthropic.TextBlock); ok {
			text += b.Text
		}
	}

	verdicts := parseSongVerdicts(text, len(guesses))
	for i := range verdicts {
		if verdicts[i].ByAI {
			verdicts[i] = finalizeClaudeVerdict(verdicts[i], 0)
			verdicts[i].Raw = strings.TrimSpace(text)
		}
	}
	return verdicts, nil
}

// songReplyFormat repeats the single-guess format once per guess, numbered, with
// the same short reason before each verdict (see replyFormat for why).
const songReplyFormat = "Reply in exactly this format and nothing else, once for every numbered guess " +
	"(each reason is at most 10 words):\n" +
	"G<number>_TITLE_REASON=<reason>\nG<number>_TITLE=<yes or no>\n" +
	"G<number>_ARTIST_REASON=<reason>\nG<number>_ARTIST=<yes or no>"

func claudeSongPrompt(title, artist string, guesses []BatchGuess) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Several players are each naming the same song. Decide, for every player, if they "+
		"*meant* the correct title and the correct artist.\n\n"+
		"Correct title: %q\nCorrect artist: %q\n\n"+
		"Their guesses:\n", title, artist)
	for i, g := range guesses {
		titleSaid := strings.ToLower(strings.TrimSpace(g.TitleGuess))
		artistSaid := strings.ToLower(strings.TrimSpace(g.ArtistGuess))
		if titleSaid == "" && artistSaid == "" {
			titleSaid = strings.ToLower(strings.TrimSpace(g.Guess))
		}
		fmt.Fprintf(&b, "Guess %d -- title: %q, artist: %q\n", i+1, titleSaid, artistSaid)
	}
	fmt.Fprintf(&b, "\n%s\n"+
		"You are seeing everyone's guesses at once so that you hold them all to one standard. "+
		"Guesses that say the same thing, up to spelling, capitalisation or a sound-alike, must get "+
		"the same verdict; a sound-alike or nickname you accept for one player you must accept for "+
		"another. Otherwise judge each guess on its own: one player being right does not make another "+
		"right, and one being wrong does not make another wrong.\n"+
		"Score title and artist independently. A yes on one does not change the other. "+
		"Do not decide whether anyone earned a token.\n"+
		"An empty title guess is TITLE=no. An empty artist guess is ARTIST=no.\n\n%s",
		claudeIntentRules, songReplyFormat)
	return b.String()
}

var songVerdictLine = regexp.MustCompile(`^G(\d+)_(TITLE|ARTIST)=(YES|NO)$`)

// parseSongVerdicts reads a numbered reply into n verdicts. A guess missing
// either its title or its artist line is returned with ByAI false, which is how
// the caller knows to judge that one locally rather than trust half a verdict.
func parseSongVerdicts(text string, n int) []Verdict {
	type seen struct{ title, artist, titleOK, artistOK bool }
	reads := make([]seen, n)

	for _, line := range strings.Split(strings.ToUpper(text), "\n") {
		m := songVerdictLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		index, err := strconv.Atoi(m[1])
		if err != nil || index < 1 || index > n {
			continue
		}
		yes := m[3] == "YES"
		if m[2] == "TITLE" {
			reads[index-1].title, reads[index-1].titleOK = yes, true
		} else {
			reads[index-1].artist, reads[index-1].artistOK = yes, true
		}
	}

	verdicts := make([]Verdict, n)
	for i, r := range reads {
		if !r.titleOK || !r.artistOK {
			continue
		}
		verdicts[i] = Verdict{TitleCorrect: r.title, ArtistCorrect: r.artist, ByAI: true}
	}
	return verdicts
}
