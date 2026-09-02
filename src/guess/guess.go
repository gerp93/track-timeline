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
	"log"
	"time"
)

// Input is one player's guess and the authored answer it is judged against.
type Input struct {
	// Guess is what the player typed, free-form. Expect "bowie heroes",
	// "Heroes by David Bowie", "herose", or just "bowie".
	Guess string
	// Title and Artist are the card's authored values.
	Title  string
	Artist string
}

// Verdict is the outcome. Title and artist are scored independently so the
// game can decide how strict to be without a Judge having an opinion about it.
type Verdict struct {
	TitleCorrect  bool
	ArtistCorrect bool
	// Explanation is optional and shown only to the player who guessed. A
	// Judge that has nothing useful to say should leave it empty rather than
	// inventing something.
	Explanation string
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
	timed, cancel := context.WithTimeout(ctx, judgeTimeout)
	defer cancel()

	verdict, err := configured.Judge(timed, in)
	if err == nil {
		return verdict
	}

	log.Printf("guess: judge failed (%v); falling back to local matching", err)
	verdict, err = fallback.Judge(context.Background(), in)
	if err != nil {
		// Normalized never returns an error, so this is unreachable in
		// practice; scoring nothing correct is the safe reading if it ever is.
		log.Printf("guess: fallback judge also failed: %v", err)
		return Verdict{}
	}
	return verdict
}
