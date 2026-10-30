package database

import (
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
)

// sessionBreak is the longest gap between one game ending and the next that
// still counts as the same sitting. A finished game plus a short break is about
// three quarters of an hour at the usual table, so an hour keeps a night's games
// together without joining a lunchtime game to an evening one.
const sessionBreak = time.Hour

// bounceBackMisses is how many misses in a row a hit has to follow to be a bounce
// back.
const bounceBackMisses = 3

// eventTotals is what the event log holds for one player and one event type.
type eventTotals struct {
	Count int
	Max   int
	Min   int
}

// userEventTotals maps user -> event type -> totals.
type userEventTotals map[uuid.UUID]map[string]eventTotals

// loadAchievementEvents reads the whole event log, summed per player and type.
func loadAchievementEvents() (userEventTotals, error) {
	rows, err := query(`
		SELECT USER_ID, EVENT_TYPE, COUNT(*), MAX(VALUE), MIN(VALUE)
		FROM TRACK_TIMELINE_LOG_ACHIEVEMENT_EVENT
		GROUP BY USER_ID, EVENT_TYPE
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	totals := make(userEventTotals)
	for rows.Next() {
		var userId uuid.UUID
		var eventType string
		var t eventTotals
		if err := rows.Scan(&userId, &eventType, &t.Count, &t.Max, &t.Min); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		if totals[userId] == nil {
			totals[userId] = make(map[string]eventTotals)
		}
		totals[userId][eventType] = t
	}
	return totals, nil
}

// buildAchievementMetrics works out every approved player's numbers from the
// stats snapshot, the games rebuilt from it and the event log. It is a pure
// function, like buildStatsReport, so the arithmetic is covered by tests with no
// database. The definitions match the statistics pages: placements are own-turn
// unless they say steal, solo games count toward nothing but accuracy, and a
// guest or deleted account has no numbers.
func buildAchievementMetrics(s statsSnapshot, games []StatsGame, events userEventTotals) map[uuid.UUID]Metrics {
	metrics := make(map[uuid.UUID]Metrics)
	metricsFor := func(id uuid.UUID) Metrics {
		user, ok := s.users[id]
		if !ok || !user.Approved {
			return nil
		}
		if m, ok := metrics[id]; ok {
			return m
		}
		m := make(Metrics)
		metrics[id] = m
		return m
	}
	// Every approved account gets an entry, so a player with no history at all
	// still reads as zeros rather than as missing.
	for id, user := range s.users {
		if user.Approved {
			metricsFor(id)
		}
	}

	// Placements, in the order they were played.
	type turnRun struct {
		hits, misses int
	}
	runs := make(map[uuid.UUID]*turnRun)
	decades := make(map[uuid.UUID]map[int]int)
	genres := make(map[uuid.UUID]map[string]bool)
	for _, p := range s.placements {
		m := metricsFor(p.UserId)
		if m == nil {
			continue
		}
		if p.Steal {
			if p.Correct {
				m[MetricStealsWon]++
			}
			continue
		}

		run := runs[p.UserId]
		if run == nil {
			run = &turnRun{}
			runs[p.UserId] = run
		}
		if p.Correct {
			if run.misses >= bounceBackMisses {
				m[MetricBounceBack]++
			}
			run.hits++
			run.misses = 0
			m[MetricBestStreak] = max(m[MetricBestStreak], run.hits)

			if decades[p.UserId] == nil {
				decades[p.UserId] = make(map[int]int)
			}
			decades[p.UserId][decadeOf(p.Year)]++
			if card, ok := s.cards[p.CardId]; ok {
				if genre := primaryGenre(card.Genre); genre != "" {
					if genres[p.UserId] == nil {
						genres[p.UserId] = make(map[string]bool)
					}
					genres[p.UserId][genre] = true
				}
			}
		} else {
			run.misses++
			run.hits = 0
			m[MetricMissStreak] = max(m[MetricMissStreak], run.misses)
		}
	}
	for id, byDecade := range decades {
		m := metrics[id]
		for decade, n := range byDecade {
			m[DecadeMetric(decade)] = n
			if n >= TimeTravelerPerDecade {
				m[MetricDecades15]++
			}
		}
	}
	for id, set := range genres {
		metrics[id][MetricGenres] = len(set)
	}

	// Guesses.
	bothRun := make(map[uuid.UUID]int)
	for _, g := range s.guesses {
		m := metricsFor(g.UserId)
		if m == nil {
			continue
		}
		if g.Title && g.Artist {
			m[MetricBoth]++
			bothRun[g.UserId]++
			m[MetricBothStreak] = max(m[MetricBothStreak], bothRun[g.UserId])
		} else {
			bothRun[g.UserId] = 0
		}
		if g.Artist && !g.Title {
			m[MetricArtistOnly]++
		}
		if g.Jason {
			m[MetricJasonTotal]++
			if g.Artist {
				m[MetricJasonRight]++
			} else {
				m[MetricJasonWrong]++
			}
		}
	}

	// Cards bought with tokens, where the buyer was on record.
	for _, e := range s.events {
		if e.Type != CardEventBought || !e.UserId.Valid {
			continue
		}
		if m := metricsFor(e.UserId.UUID); m != nil {
			m[MetricBought]++
		}
	}

	// Exact-year wagers: a right year pays the stake back doubled (net +stake), a
	// wrong one loses it (net -stake).
	wagerRun := make(map[uuid.UUID]int)
	wagerNet := make(map[uuid.UUID]int)
	for _, w := range s.wagers {
		m := metricsFor(w.UserId)
		if m == nil {
			continue
		}
		m[MetricWagers]++
		if w.Correct {
			m[MetricWagerHits]++
			wagerRun[w.UserId]++
			m[MetricWagerStreak] = max(m[MetricWagerStreak], wagerRun[w.UserId])
			m[MetricWagerBigWin] = max(m[MetricWagerBigWin], w.Tokens)
			wagerNet[w.UserId] += w.Tokens
		} else {
			wagerRun[w.UserId] = 0
			m[MetricWagerBigLoss] = max(m[MetricWagerBigLoss], w.Tokens)
			wagerNet[w.UserId] -= w.Tokens
		}
	}
	for id, net := range wagerNet {
		if net > 0 {
			metrics[id][MetricWagerNetUp] = net
		} else if net < 0 {
			metrics[id][MetricWagerNetDown] = -net
		}
	}

	// Games, oldest first.
	winStreak := make(map[uuid.UUID]int)
	chain := make(map[uuid.UUID]int)
	lastEnd := make(map[uuid.UUID]time.Time)
	online := make(map[uuid.UUID]bool)
	inPerson := make(map[uuid.UUID]bool)
	for _, game := range games {
		if game.Solo() {
			continue
		}
		for _, id := range game.Players {
			m := metricsFor(id)
			if m == nil {
				continue
			}
			m[MetricGames]++

			if last, ok := lastEnd[id]; ok && game.WonOn.Sub(last) <= sessionBreak {
				chain[id]++
			} else {
				chain[id] = 1
			}
			lastEnd[id] = game.WonOn
			m[MetricSessionGames] = max(m[MetricSessionGames], chain[id])

			if id != game.WinnerId {
				winStreak[id] = 0
				continue
			}
			m[MetricWins]++
			winStreak[id]++
			m[MetricWinStreak] = max(m[MetricWinStreak], winStreak[id])
			m[MetricTableWin] = max(m[MetricTableWin], game.PlayerCount)
			if game.Room.Valid {
				if game.Room.Bool {
					inPerson[id] = true
				} else {
					online[id] = true
				}
			}
		}
	}
	for id := range metrics {
		worlds := 0
		if online[id] {
			worlds++
		}
		if inPerson[id] {
			worlds++
		}
		if worlds > 0 {
			metrics[id][MetricWorlds] = worlds
		}
	}

	// The event log: each type as a count, and its biggest and smallest VALUE for
	// the achievements measured on a number rather than on how often.
	for id, byType := range events {
		m := metricsFor(id)
		if m == nil {
			continue
		}
		for eventType, t := range byType {
			m[eventType] = t.Count
			m[eventType+metricEventMaxSuffix] = t.Max
			m[eventType+metricEventMinSuffix] = t.Min
		}
	}

	return metrics
}
