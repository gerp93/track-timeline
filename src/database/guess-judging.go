package database

import (
	"context"
	"errors"
	"log"
	"sync"

	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/guess"
)

// gameJudgeLocks serializes JudgeRoundGuesses per game, so two ways of ending a
// round at once (a steal attempt and its timeout) cannot both judge, and so both
// pay, the same guesses. Per game rather than global: judging waits on a network
// call, and one lobby's round must not queue behind another's.
var gameJudgeLocks sync.Map

func judgeLockFor(gameId uuid.UUID) *sync.Mutex {
	lock, _ := gameJudgeLocks.LoadOrStore(gameId, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// JudgeRoundGuesses judges every guess of the round that has not been judged yet,
// and pays the tokens they earned. It is what a round's end does with the guesses
// players typed along the way: nothing is judged as it arrives.
//
// A round can span several songs (every song skipped before the one that is
// finally placed), and a guess is judged against the song it was typed for. Each
// song's guesses go to the judge together, in one request, so it sees everything
// said about that song and holds it all to one standard; the songs themselves are
// judged concurrently. A draft the player never locked is judged like any other:
// whatever was in the boxes is their attempt.
//
// Safe to call more than once: only guesses not yet judged are touched, so a
// second call (the other half of a race to end the round) finds nothing to do.
func JudgeRoundGuesses(gameId uuid.UUID) error {
	lock := judgeLockFor(gameId)
	lock.Lock()
	defer lock.Unlock()

	rows, err := query(guessSelect+" WHERE G.TRACK_TIMELINE_GAME_ID = ? AND G.JUDGED = 0 AND G.GUESS_TEXT <> ''"+guessOrder, gameId)
	if err != nil {
		return err
	}
	pending, err := scanGuesses(rows)
	rows.Close()
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}

	// Group by song, keeping the order songs were played in.
	var cards []uuid.UUID
	byCard := make(map[uuid.UUID][]int)
	for i, g := range pending {
		if _, seen := byCard[g.CardId]; !seen {
			cards = append(cards, g.CardId)
		}
		byCard[g.CardId] = append(byCard[g.CardId], i)
	}

	verdicts := make([]guess.Verdict, len(pending))
	var wg sync.WaitGroup
	for _, cardId := range cards {
		indexes := byCard[cardId]
		wg.Add(1)
		go func() {
			defer wg.Done()
			batch := make([]guess.BatchGuess, len(indexes))
			for k, i := range indexes {
				batch[k] = guess.BatchGuess{
					TitleGuess:  pending[i].TitleGuess,
					ArtistGuess: pending[i].ArtistGuess,
					Guess:       pending[i].GuessText,
				}
			}
			first := pending[indexes[0]]
			for k, v := range guess.AdjudicateSong(context.Background(), first.SongTitle, first.SongArtist, batch) {
				verdicts[indexes[k]] = v
			}
		}()
	}
	wg.Wait()

	var firstErr error
	for i, g := range pending {
		v := verdicts[i]
		g.TitleCorrect = v.TitleCorrect
		g.ArtistCorrect = v.ArtistCorrect
		g.TitleMatchPercent = int(v.TitleMatchPercent)
		g.ArtistMatchPercent = int(v.ArtistMatchPercent)
		g.JudgedByAI = v.ByAI
		tokens := GuessTokensEarned(g)

		// Marked judged before it is paid: the row is what stops a second pass
		// paying it again.
		if err := execute(`
			UPDATE TRACK_TIMELINE_TITLE_GUESS
			SET JUDGED = 1, LOCKED = 1, TITLE_CORRECT = ?, ARTIST_CORRECT = ?,
				TITLE_MATCH_PERCENT = ?, ARTIST_MATCH_PERCENT = ?, JUDGED_BY_AI = ?, TOKENS_AWARDED = ?
			WHERE ID = ? AND JUDGED = 0`,
			g.TitleCorrect, g.ArtistCorrect, g.TitleMatchPercent, g.ArtistMatchPercent, g.JudgedByAI, tokens, g.Id,
		); err != nil {
			log.Println(err)
			firstErr = errors.New("failed to record a guess verdict")
			continue
		}
		if _, err := AwardGuessToken(gameId, g.PlayerId, g); err != nil {
			// The guess still stands as judged, so this is logged loudly.
			log.Println("failed to award guess tokens:", err)
		}
		if err := LogTitleGuess(g.UserId, g.CardId, g.GuessText, g.TitleCorrect, g.ArtistCorrect); err != nil {
			log.Println(err)
		}
	}
	return firstErr
}

// SettleGuesses judges whatever has not been judged and returns every guess of the
// round, for a round that ends without a card being awarded and so does not pass
// through resolveRound (the player on turn ran out of time).
func SettleGuesses(gameId uuid.UUID) ([]Guess, error) {
	if err := JudgeRoundGuesses(gameId); err != nil {
		log.Println(err)
	}
	return GetGuesses(gameId)
}
