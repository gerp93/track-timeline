# The guess package

This package answers one question: **did the player name the song?**

It ships with a working local implementation so the game is playable
immediately. Swapping in a model-backed one is a single small file plus one
line in `main.go` — that's the whole point of the seam, and it's left for you
to write.

## The interface

```go
type Input struct {
    Guess  string // what the player typed, free-form
    Title  string // the card's authored title
    Artist string // the card's authored artist
}

type Verdict struct {
    TitleCorrect  bool
    ArtistCorrect bool
    Explanation   string // optional, shown only to the guessing player
}

type Judge interface {
    Judge(ctx context.Context, in Input) (Verdict, error)
}
```

Three strings in, two booleans out. A `Judge` knows nothing about games,
players, lobbies, rounds or tokens, so you can build and test one entirely on
its own.

## What ships by default

`Normalized` folds case and accents, strips punctuation, bracketed asides
(`(Remastered 2011)`), featured-artist clauses and a leading "the", then checks
how much of the authored title and artist appear in what the player typed. It
tolerates small typos, including letter transpositions.

It handles the ordinary cases well and is deliberately not clever. It does not
know that "MJ" is Michael Jackson, that "the Fab Four" is the Beatles, or that
"the one from Guardians of the Galaxy" is a real attempt at an answer. That gap
is the reason to reach for a model.

## Where it plugs in

`Adjudicate` wraps the configured `Judge` in a 5-second timeout and **falls
back to `Normalized` on any error**. This matters more than it looks: a missing
API key, an exhausted account balance, a rate limit or a network blip degrades
the quality of judging instead of stalling the round for everyone in the lobby.
Whatever you write, that safety net stays.

## Writing a Claude-backed judge

### 1. Add the SDK

```sh
cd src && go get github.com/anthropics/anthropic-sdk-go
```

### 2. Create `src/guess/claude.go`

Sketch — the parts to get right are the constrained output format and the
strict parse, because anything looser turns a boolean question into free text
you then have to guess at:

```go
package guess

import (
    "context"
    "errors"
    "fmt"
    "strings"

    "github.com/anthropics/anthropic-sdk-go"
)

// ClaudeJudge asks a model whether a free-form guess names the song.
type ClaudeJudge struct {
    client anthropic.Client
}

func NewClaudeJudge() ClaudeJudge {
    // Reads ANTHROPIC_API_KEY from the environment.
    return ClaudeJudge{client: anthropic.NewClient()}
}

func (j ClaudeJudge) Judge(ctx context.Context, in Input) (Verdict, error) {
    prompt := fmt.Sprintf(
        "A player is guessing a song that is playing.\n\n"+
            "Correct title: %q\nCorrect artist: %q\nThe player said: %q\n\n"+
            "Judge leniently, the way a friendly quizmaster would: accept "+
            "misspellings, nicknames, well-known abbreviations, partial "+
            "titles, and a missing featured artist. Do not accept a guess "+
            "that names a different song or a different performer.\n\n"+
            "Reply with exactly one line and nothing else, in this form:\n"+
            "TITLE=yes|no ARTIST=yes|no",
        in.Title, in.Artist, in.Guess,
    )

    message, err := j.client.Messages.New(ctx, anthropic.MessageNewParams{
        Model:     "claude-haiku-4-5",
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

    return parseVerdict(text)
}

// parseVerdict refuses to guess. An unreadable reply is an error, which sends
// Adjudicate to the local fallback -- far better than silently scoring a
// malformed answer as "no".
func parseVerdict(text string) (Verdict, error) {
    fields := strings.Fields(strings.ToUpper(strings.TrimSpace(text)))
    var verdict Verdict
    var sawTitle, sawArtist bool

    for _, field := range fields {
        switch {
        case strings.HasPrefix(field, "TITLE="):
            verdict.TitleCorrect = strings.TrimPrefix(field, "TITLE=") == "YES"
            sawTitle = true
        case strings.HasPrefix(field, "ARTIST="):
            verdict.ArtistCorrect = strings.TrimPrefix(field, "ARTIST=") == "YES"
            sawArtist = true
        }
    }

    if !sawTitle || !sawArtist {
        return Verdict{}, errors.New("could not read a verdict from the model reply")
    }
    return verdict, nil
}
```

### 3. Flip the line in `main.go`

```go
guess.SetJudge(guess.NewClaudeJudge())
```

That's it. Nothing else in the game changes.

## Things worth knowing before you start

**Model choice.** `claude-haiku-4-5` is the right default here: this is a
short classification with no multi-step reasoning, and it is the fastest and
cheapest option. Extended thinking would be wasted on it.

**Latency is part of the game.** The guess happens while a song is playing and
other players are waiting, so keep `MaxTokens` small and do not add thinking.
The 5-second timeout in `Adjudicate` is the backstop, not the target.

**Cost is negligible at this scale.** Roughly 150 input and 30 output tokens per
guess. At Haiku pricing that is on the order of a few cents per two hundred
guesses.

**Billing is separate.** API usage is pay-as-you-go from console.anthropic.com
and is not connected to any Claude subscription. Set a spending limit while
you're there — it costs nothing and removes the "what if something loops"
worry entirely.

**Test it without spending anything.** `Judge` takes an interface, so a fake
implementation in a test needs no network and no key. See
`normalized_test.go`'s `failingJudge` for the shape.

## Refinements, once it works

- **Constrain the output structurally** rather than by asking for a format.
  The SDK supports strict tool definitions and structured outputs, either of
  which makes `parseVerdict` unnecessary. Check the SDK's current Go bindings
  for the exact call — they are worth using once the basic version works.
- **Log and review.** `TRACK_TIMELINE_LOG_TITLE_GUESS` keeps every guess with
  its verdict, so you can go back and see where the judging was wrong and tune
  the prompt against what people actually typed.
- **Return an `Explanation`.** It is surfaced privately to the guessing player,
  so "close — right artist, wrong album" is a nice touch the local judge cannot
  manage.
