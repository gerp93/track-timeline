// Package guess decides whether a player's free-form guess names the song that
// is playing.
//
// It is deliberately the narrowest thing that could work: a Judge is handed
// three strings and answers two booleans. It knows nothing about games,
// players, lobbies, tokens or rounds, so an implementation can be written and
// tested entirely on its own — including one that calls out to a language
// model, which is the reason the seam exists at all. See README.md in this
// directory for exactly what to write.
package guess

import (
	"context"
	"errors"
	"log"
	"time"
)

// Input is one player's guess and the authored answer it is judged against.
type Input struct {
	// Guess is what the player typed, free-form. Expect "bowie heroes",
	// "Heroes by David Bowie", "herose", or just "bowie". Used when
	// TitleGuess / ArtistGuess are empty (one box, or a Judge that only
	// sees a single string).
	Guess string
	// TitleGuess and ArtistGuess are the split form fields. When either is
	// set, the local judge scores title against TitleGuess and artist
	// against ArtistGuess so a correct artist name cannot inflate the
	// title's match percent (and vice versa).
	TitleGuess  string
	ArtistGuess string
	// Title and Artist are the card's authored values.
	Title  string
	Artist string
	// MinMatchPercent is the lobby's coverage bar (60/70/80/90): this
	// fraction of authored title/artist words must match. Zero means 60.
	MinMatchPercent int
	// TitleOnly is title-only lobby mode: the artist is not guessed, so
	// Claude is not asked about it. Token rules (both / either / title)
	// stay in the game layer after this returns.
	TitleOnly bool
}

// Verdict is the outcome. Title and artist are scored independently so the
// game can decide how strict to be without a Judge having an opinion about it.
type Verdict struct {
	TitleCorrect  bool
	ArtistCorrect bool
	// TitleMatchPercent and ArtistMatchPercent are 0-100, shown to the player
	// alongside the correct/incorrect verdict. They are best-effort: a Judge
	// that cannot produce a meaningful percentage should report 100 or 0 to
	// match its own boolean, rather than leave this looking like a real score
	// it isn't.
	TitleMatchPercent  float64
	ArtistMatchPercent float64
	// Explanation is optional and shown only to the player who guessed. A
	// Judge that has nothing useful to say should leave it empty rather than
	// inventing something.
	Explanation string
	// ByAI is true when Claude produced this verdict. Chat copy skips match
	// percents in that case. False for the local matcher and for any Claude
	// attempt that fell back to it.
	ByAI bool
}

// Judge decides whether a guess names the song.
//
// Implementations must be safe for concurrent use by multiple goroutines, and
// must respect ctx: a Judge that blocks holds up the round for everyone in the
// lobby, not just the player who guessed.
type Judge interface {
	Judge(ctx context.Context, in Input) (Verdict, error)
}

// judgeTimeout bounds any single adjudication. Generous enough for a network
// round-trip, short enough that a hung call does not visibly stall play.
const judgeTimeout = 5 * time.Second

// fallback is used when the configured Judge fails or times out. It is always
// the local implementation, never whatever was configured, so a Judge that is
// down cannot take the game down with it.
var fallback = Normalized{}

var configured Judge = Normalized{}

// SetJudge installs the Judge the game will use. Call once at startup, before
// serving. Passing nil restores the built-in local judge.
//
// This is the one line to change to swap in a different implementation.
func SetJudge(j Judge) {
	if j == nil {
		configured = Normalized{}
		return
	}
	configured = j
}

// Adjudicate runs the configured Judge under a timeout, falling back to the
// local judge on any error.
//
// The fallback is the point: a missing API key, an exhausted balance, a network
// blip or a slow response degrades the quality of judging rather than blocking
// the round. Players get a slightly stricter verdict instead of an error.
func Adjudicate(ctx context.Context, in Input) Verdict {
	return runJudge(ctx, configured, in)
}

// AdjudicateKind runs the lobby's chosen judge. "claude" uses the Anthropic
// API when a key is configured; anything else (including a missing key) uses
// the local word matcher. Errors still fall back to Normalized. A successful
// Claude call sets Verdict.ByAI so chat can attribute it without percents.
func AdjudicateKind(ctx context.Context, in Input, kind string) Verdict {
	if kind == JudgeClaude {
		if claude, ok := defaultClaudeJudge(); ok {
			timed, cancel := context.WithTimeout(ctx, judgeTimeout)
			defer cancel()
			verdict, err := claude.Judge(timed, in)
			if err == nil {
				verdict.ByAI = true
				return verdict
			}
			log.Printf("guess: judge failed (%v); falling back to local matching", err)
		} else {
			log.Printf("guess: Claude requested but no API key; using local matching")
		}
	}
	return runJudge(ctx, Normalized{}, in)
}

// AdjudicateClaude calls Claude and returns its error instead of falling back
// to the local matcher. The admin guess tester uses this so a failed API call
// is visible next to the heuristic, not silently replaced by it.
func AdjudicateClaude(ctx context.Context, in Input) (Verdict, error) {
	j, ok := defaultClaudeJudge()
	if !ok {
		return Verdict{}, errors.New("Claude API key is not configured")
	}
	return j.Judge(ctx, in)
}

// meetsMatchBar is how a 0–100 score becomes a win: at or above the lobby
// percent. Claude maps yes/no onto 100/0 then uses this; the local judge uses
// it on word coverage.
func meetsMatchBar(percent float64, minMatchPercent int) bool {
	if minMatchPercent <= 0 {
		minMatchPercent = 60
	}
	return percent >= float64(minMatchPercent)
}

func runJudge(ctx context.Context, j Judge, in Input) Verdict {
	timed, cancel := context.WithTimeout(ctx, judgeTimeout)
	defer cancel()

	verdict, err := j.Judge(timed, in)
	if err == nil {
		return verdict
	}

	log.Printf("guess: judge failed (%v); falling back to local matching", err)
	verdict, err = fallback.Judge(context.Background(), in)
	if err != nil {
		log.Printf("guess: fallback judge also failed: %v", err)
		return Verdict{}
	}
	return verdict
}
