# The guess package

This package answers one question: **did the player name the song?**

It ships with a working local implementation so the game is playable
immediately. A Claude intent judge lives in `claude.go` and is chosen per lobby
(or on Quizmaster Testing), not by a global `SetJudge` in `main.go`.

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

## Claude intent judge

`claude.go` asks Haiku whether the player *meant* the authored title and
artist. Typos, nicknames, and messy wording are yes; a different song or
performer is no. Match percents are 100 or 0 to match that boolean. An
unreadable model reply is an error, which sends `AdjudicateKind` to the local
fallback — far better than silently scoring a malformed answer as "no".

Set `TRACK_TIMELINE_ANTHROPIC_API_KEY` (or `ANTHROPIC_API_KEY`) on the server.
New Game and Quizmaster Testing then offer **AI Judge** yes/no. No uses the local
word matcher at the chosen match percent. Yes uses Claude, and the same
percent is the heuristic fallback if the API errors or the reply is unreadable.
Without a key, Yes is disabled.

Gameplay uses `AdjudicateKind` with the lobby's `GUESS_JUDGE`. `SetJudge` is
still unused; the default `Adjudicate` path stays local so tests do not need a
key.

## Things worth knowing

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
