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
	// Raw is the model's unparsed reply, kept only so the Quizmaster Testing
	// page and the server log can show exactly what Claude said. Empty for the
	// local matcher.
	Raw string
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

// AdjudicateGuess judges a guess the way every game does: Claude decides
// whenever the API key is configured and the call succeeds, and the local word
// matcher is only the fallback for a missing key, an API error, a timeout or an
// unreadable reply. A successful Claude call sets Verdict.ByAI so chat can
// attribute it without percents.
func AdjudicateGuess(ctx context.Context, in Input) Verdict {
	if claude, ok := defaultClaudeJudge(); ok {
		timed, cancel := context.WithTimeout(ctx, judgeTimeout)
		defer cancel()
		verdict, err := claude.Judge(timed, in)
		if err == nil {
			verdict.ByAI = true
			return withHeuristicFloor(ctx, in, verdict)
		}
		log.Printf("guess: judge failed (%v); falling back to local matching", err)
	}
	return runJudge(ctx, Normalized{}, in)
}

// withHeuristicFloor lets Claude add matches the word matcher cannot see
// (nicknames, sound-alikes, "1000" for "A Thousand") but not veto ones it can:
// a title or artist the local matcher already accepts stays correct even if
// the model said no. The matcher is conservative about wrong answers, so this
// only rescues plainly right guesses, and any disagreement is logged.
func withHeuristicFloor(ctx context.Context, in Input, claude Verdict) Verdict {
	local, err := fallback.Judge(ctx, in)
	if err != nil {
		return claude
	}
	if local.TitleCorrect && !claude.TitleCorrect {
		log.Printf("guess: Claude rejected a title the local matcher accepts (%q vs %q, model replied %q); keeping it", in.TitleGuess, in.Title, claude.Raw)
		claude.TitleCorrect = true
		claude.TitleMatchPercent = local.TitleMatchPercent
	}
	if local.ArtistCorrect && !claude.ArtistCorrect {
		log.Printf("guess: Claude rejected an artist the local matcher accepts (%q vs %q, model replied %q); keeping it", in.ArtistGuess, in.Artist, claude.Raw)
		claude.ArtistCorrect = true
		claude.ArtistMatchPercent = local.ArtistMatchPercent
	}
	return claude
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
