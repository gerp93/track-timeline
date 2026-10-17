package database

import (
	"sort"
	"time"

	"github.com/google/uuid"
)

// The log tables carry no game id (they are FK-free on purpose, see their DDL),
// so the stats pages rebuild games from what the logs do have. A win row closes
// a game; the placements between the previous win and this one belong to it.
// That is exact when one lobby plays at a time, which is how this game is
// played, and is cross-checked in practice: the number of distinct players
// found in each rebuilt game matches the win row's own PLAYER_COUNT.
//
// Two bounds keep it honest. gameLookback stops a game from swallowing the
// leftovers of an earlier abandoned one (a game nobody finished writes no win
// row, so its placements would otherwise be glued onto the next game played
// after it). sessionGap is the quiet spell that separates one play session from
// the next.
const (
	gameLookback = 3 * time.Hour
	sessionGap   = 4 * time.Hour
	sessionGrace = 15 * time.Minute
)

// statsPlacement is one row of TRACK_TIMELINE_LOG_PLACEMENT.
type statsPlacement struct {
	UserId  uuid.UUID
	CardId  uuid.UUID
	At      time.Time
	Year    int
	Steal   bool
	Correct bool
}

// statsWin is one row of TRACK_TIMELINE_LOG_WIN.
type statsWin struct {
	UserId      uuid.UUID
	At          time.Time
	CardsToWin  int
	PlayerCount int
}

// StatsGame is one finished game, rebuilt from the logs.
type StatsGame struct {
	StartedOn   time.Time
	WonOn       time.Time
	WinnerId    uuid.UUID
	WinnerName  string
	WinnerKnown bool // the winner is an approved account with a stats page
	CardsToWin  int
	PlayerCount int
	Players     []uuid.UUID
	Placements  int
	Minutes     int
}

// Solo reports a game played alone; those count toward accuracy but not toward
// wins, win rates or the leaderboard, since nobody was beaten.
func (g StatsGame) Solo() bool { return g.PlayerCount < 2 }

// reconstructGames turns the win and placement logs into games. Both inputs
// must be sorted by time ascending.
func reconstructGames(wins []statsWin, placements []statsPlacement) []StatsGame {
	games := make([]StatsGame, 0, len(wins))
	var previous time.Time

	for _, win := range wins {
		lower := previous
		if floor := win.At.Add(-gameLookback); floor.After(lower) {
			lower = floor
		}
		previous = win.At

		// First placement strictly after lower, then everything up to and
		// including the win's own timestamp.
		first := sort.Search(len(placements), func(i int) bool { return placements[i].At.After(lower) })

		game := StatsGame{
			StartedOn:   win.At,
			WonOn:       win.At,
			WinnerId:    win.UserId,
			CardsToWin:  win.CardsToWin,
			PlayerCount: win.PlayerCount,
		}
		seen := make(map[uuid.UUID]bool)
		for i := first; i < len(placements) && !placements[i].At.After(win.At); i++ {
			p := placements[i]
			if game.Placements == 0 {
				game.StartedOn = p.At
			}
			game.Placements++
			if !seen[p.UserId] {
				seen[p.UserId] = true
				game.Players = append(game.Players, p.UserId)
			}
		}
		if !seen[win.UserId] {
			game.Players = append(game.Players, win.UserId)
		}
		game.Minutes = int(win.At.Sub(game.StartedOn).Round(time.Minute) / time.Minute)
		games = append(games, game)
	}

	return games
}

// StatsSession is one sitting's play: a run of placements with no gap longer than
// sessionGap between them.
type StatsSession struct {
	StartedOn  time.Time
	EndedOn    time.Time
	Games      int
	Players    int
	Placements int
	Turn       Rate
}

// buildSessions groups the placement log into play sessions, oldest first. Both
// inputs must be sorted by time ascending.
func buildSessions(placements []statsPlacement, wins []statsWin) []StatsSession {
	sessions := make([]StatsSession, 0)
	var players map[uuid.UUID]bool

	for _, p := range placements {
		if len(sessions) == 0 || p.At.Sub(sessions[len(sessions)-1].EndedOn) > sessionGap {
			sessions = append(sessions, StatsSession{StartedOn: p.At})
			players = make(map[uuid.UUID]bool)
		}
		session := &sessions[len(sessions)-1]
		session.EndedOn = p.At
		session.Placements++
		players[p.UserId] = true
		session.Players = len(players)
		if !p.Steal {
			session.Turn.tally(p.Correct)
		}
	}

	for _, win := range wins {
		// A win is logged moments after the placement that clinched it.
		i := sort.Search(len(sessions), func(i int) bool { return sessions[i].EndedOn.Add(sessionGrace).After(win.At) })
		if i < len(sessions) && !win.At.Before(sessions[i].StartedOn) {
			sessions[i].Games++
		}
	}

	return sessions
}

// longestStreaks returns each user's longest run of correct own-turn
// placements, in the order they were played. Steals are left out: a steal is
// optional and only available after somebody else missed, so it says nothing
// about holding a streak. Input must be sorted by time ascending.
func longestStreaks(placements []statsPlacement) map[uuid.UUID]int {
	best := make(map[uuid.UUID]int)
	run := make(map[uuid.UUID]int)

	for _, p := range placements {
		if p.Steal {
			continue
		}
		if p.Correct {
			run[p.UserId]++
			if run[p.UserId] > best[p.UserId] {
				best[p.UserId] = run[p.UserId]
			}
		} else {
			run[p.UserId] = 0
		}
	}

	return best
}

// median returns the middle value (mean of the two middle values for an even
// count), or 0 for no values. Used for "typical game" figures because one
// abandoned-then-resumed marathon would drag a mean far from what a game
// usually feels like.
func median(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid] + 1) / 2
}

// decadeOf floors a year to its decade (1987 -> 1980).
func decadeOf(year int) int { return year / 10 * 10 }
