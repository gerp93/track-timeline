package database

import (
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The statistics pages are computed, not stored. One pass reads the append-only
// LOG_* tables into a snapshot (loadStatsSnapshot), a pure function turns the
// snapshot into a StatsReport (buildStatsReport), and each page picks the part
// it needs. That keeps every figure on every page derived from the same
// definitions, and keeps the arithmetic testable without a database.
//
// Definitions that apply everywhere:
//
//   - Placement accuracy counts OWN-TURN placements only. A steal is attempted
//     on a song somebody else just missed, so steals are a harder, self-selected
//     sample and are reported on their own line (Steal) instead of being
//     blended in.
//   - Only approved accounts get a player page or a leaderboard row; room guests
//     (unapproved) and deleted users still count toward table-wide totals and
//     song statistics, because what they did did happen.
//   - Games are rebuilt from the win log (see stats-games.go). Games nobody
//     finished leave no trace there, so "games" always means finished games.

// smoothingStrength is how many phantom attempts at the table average a song's
// record is blended with when ranking songs by difficulty.
const smoothingStrength = 4.0

// minSongTries is how many own-turn placements a song needs before it can be
// ranked hardest/easiest at all. At the usual ~74% accuracy a song missed twice
// in two tries is not unusual (a few among a hundred songs will do it by luck),
// whereas three misses in three is.
const minSongTries = 3

// minGuessesForRecognition is how many guesses a song needs before it can be
// called well- or little-known.
const minGuessesForRecognition = 3

// Song list views, in the order the Songs tab offers them.
const (
	SongViewHardest = "hardest"
	SongViewEasiest = "easiest"
	SongViewPlayed  = "played"
	SongViewUnknown = "unknown"
	SongViewSkipped = "skipped"
)

// statsGuess is one row of TRACK_TIMELINE_LOG_TITLE_GUESS, minus the text.
type statsGuess struct {
	UserId uuid.UUID
	CardId uuid.UUID
	At     time.Time
	Title  bool
	Artist bool
}

// statsCardEvent is one row of TRACK_TIMELINE_LOG_CARD.
type statsCardEvent struct {
	CardId uuid.UUID
	Type   string
}

// statsUser is the slice of USER the stats need.
type statsUser struct {
	Name     string
	Approved bool
}

// statsCard is the slice of CARD (and its genre) the stats need.
type statsCard struct {
	Title  string
	Artist string
	Year   int
	Genre  string
}

// statsSnapshot is everything the report is built from.
type statsSnapshot struct {
	placements []statsPlacement
	wins       []statsWin
	guesses    []statsGuess
	events     []statsCardEvent
	users      map[uuid.UUID]statsUser
	cards      map[uuid.UUID]statsCard
}

// NamedRate is a labelled rate (a genre).
type NamedRate struct {
	Name string
	Rate Rate
}

// EraStat is one decade's record across every player.
type EraStat struct {
	Decade int
	Turn   Rate
	Steal  Rate
	Title  Rate // songs from this era whose title was named
	Artist Rate // songs from this era whose artist was named
}

// FormEntry is one finished game in a player's recent form.
type FormEntry struct {
	Won     bool
	WonOn   time.Time
	Players int
}

// VersusRow is a player's head-to-head record against one opponent: the games
// they both played and who won them. Games nobody in the pair won are counted
// in Games only.
type VersusRow struct {
	OpponentId   uuid.UUID
	OpponentName string
	Games        int
	Wins         int
	Losses       int
}

// PlayerStats is one player's full record.
type PlayerStats struct {
	UserId   uuid.UUID
	UserName string
	Rank     int // leaderboard position; 0 when they have not finished a game

	Games    int // finished games with at least one opponent
	Wins     int
	Expected float64 // wins a coin-flip player would have, from each game's table size
	SoloWins int
	LastWin  time.Time

	Turn  Rate // own-turn placements
	Steal Rate // steals attempted / steals that won the song

	Guesses int
	Title   Rate
	Artist  Rate
	Both    Rate

	LongestStreak int
	Decades       []Rate // aligned with StatsReport.DecadeAxis
	Genres        []NamedRate
	Form          []FormEntry // most recent last
	Versus        []VersusRow
}

// WinRate is wins out of finished games.
func (p PlayerStats) WinRate() Rate { return Rate{Hit: p.Wins, Total: p.Games} }

// Edge is wins above (positive) or below what chance alone would predict for the
// tables this player sat at. Four people at a table each "should" win a quarter.
func (p PlayerStats) Edge() float64 { return float64(p.Wins) - p.Expected }

// EdgeLabel is Edge for display, signed: "+1.7", "-0.4".
func (p PlayerStats) EdgeLabel() string {
	if p.Games == 0 {
		return "–"
	}
	edge := p.Edge()
	if edge > -0.05 && edge < 0.05 {
		return "0.0"
	}
	return fmt.Sprintf("%+.1f", edge)
}

// ExpectedLabel is Expected rounded to one place.
func (p PlayerStats) ExpectedLabel() string { return fmt.Sprintf("%.1f", p.Expected) }

// SongAttempt is one placement of a song.
type SongAttempt struct {
	UserId   uuid.UUID
	UserName string
	At       time.Time
	Steal    bool
	Correct  bool
}

// SongGuess is what a player typed about a song.
type SongGuess struct {
	UserName string
	Text     string
	Title    bool
	Artist   bool
	At       time.Time
}

// SongStats is one song's record.
type SongStats struct {
	CardId uuid.UUID
	Known  bool // the song still exists in a deck; deleted songs keep their stats but lose their name
	Title  string
	Artist string
	Year   int
	Genre  string

	Turn  Rate
	Steal Rate

	Guesses     int
	NamedTitle  Rate // guesses that got the title
	NamedArtist Rate // guesses that got the artist
	Recognised  Rate // title and artist hits pooled, out of two chances per guess

	Draws    int
	Skips    int
	Bought   int
	Discards int

	Smoothed   float64 // shrunk own-turn accuracy, 0-1, used to rank difficulty
	Percentile int     // % of rankable songs this one is harder than; -1 when it has too few tries to rank
	Attempts   []SongAttempt
	History    []SongGuess
}

// StatsTotals are the table-wide headline figures.
type StatsTotals struct {
	Games         int // finished games with at least one opponent
	SoloGames     int
	StandardCards int // the usual "first to N cards" target; game-length figures only compare games played to it
	Sessions      int
	Players       int
	Placements    int
	Songs         int // distinct songs placed
	Turn          Rate
	Steal         Rate
	Guesses       int
	Title         Rate
	Artist        Rate
	Both          Rate
	Draws         int
	Skips         int
	Bought        int
	Discards      int
	MedianMinutes int
	MedianPlaces  int
	FirstOn       time.Time
	LastOn        time.Time
}

// SkipRate is skipped draws out of all draws.
func (t StatsTotals) SkipRate() Rate { return Rate{Hit: t.Skips, Total: t.Draws} }

// StatsRecords are the standout single results.
type StatsRecords struct {
	StreakPlayerId   uuid.UUID
	StreakPlayerName string
	StreakLength     int
	LongestGame      *StatsGame
	ShortestGame     *StatsGame
	BiggestTable     int
}

// StatsReport is everything the stats pages draw from.
type StatsReport struct {
	Totals     StatsTotals
	Games      []StatsGame // oldest first
	Sessions   []StatsSession
	Players    []PlayerStats // by name
	DecadeAxis []int
	Eras       []EraStat // aligned with DecadeAxis
	Genres     []NamedRate
	Songs      []SongStats
	Records    StatsRecords

	TurnAccuracy float64 // table-wide own-turn accuracy, 0-1; the prior for song ranking
	users        map[uuid.UUID]statsUser
}

// DecadeLabel is "1970s".
func DecadeLabel(decade int) string { return fmt.Sprintf("%ds", decade) }

// DecadeShort is "’70s", for column headers.
func DecadeShort(decade int) string { return fmt.Sprintf("’%02ds", decade%100) }

// Label is the era's full name ("1970s").
func (e EraStat) Label() string { return DecadeLabel(e.Decade) }

// EraLabel is the decade the song came out in ("1970s"), or empty without a year.
func (s SongStats) EraLabel() string {
	if s.Year == 0 {
		return ""
	}
	return DecadeLabel(decadeOf(s.Year))
}

// AxisLabels returns the short decade labels for the report's decade axis.
func (r StatsReport) AxisLabels() []string {
	labels := make([]string, len(r.DecadeAxis))
	for i, d := range r.DecadeAxis {
		labels[i] = DecadeShort(d)
	}
	return labels
}

// nameOf renders a user for display: their name, "Guest" for an unapproved room
// account, or "Former player" for an account that no longer exists.
func (r StatsReport) nameOf(userId uuid.UUID) string {
	user, ok := r.users[userId]
	switch {
	case !ok:
		return "Former player"
	case !user.Approved:
		return "Guest"
	default:
		return user.Name
	}
}

// --- loading ---

func loadStatsSnapshot() (statsSnapshot, error) {
	snapshot := statsSnapshot{
		users: make(map[uuid.UUID]statsUser),
		cards: make(map[uuid.UUID]statsCard),
	}

	rows, err := query(`
		SELECT USER_ID, CARD_ID, CREATED_ON_DATE, RELEASE_YEAR, IS_CHALLENGE, IS_CORRECT
		FROM TRACK_TIMELINE_LOG_PLACEMENT
		ORDER BY CREATED_ON_DATE ASC, ID ASC
	`)
	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		var p statsPlacement
		if err := rows.Scan(&p.UserId, &p.CardId, &p.At, &p.Year, &p.Steal, &p.Correct); err != nil {
			rows.Close()
			log.Println(err)
			return snapshot, errors.New("failed to scan row in query results")
		}
		snapshot.placements = append(snapshot.placements, p)
	}
	rows.Close()

	rows, err = query(`
		SELECT USER_ID, CREATED_ON_DATE, CARDS_TO_WIN, PLAYER_COUNT
		FROM TRACK_TIMELINE_LOG_WIN
		ORDER BY CREATED_ON_DATE ASC, ID ASC
	`)
	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		var w statsWin
		if err := rows.Scan(&w.UserId, &w.At, &w.CardsToWin, &w.PlayerCount); err != nil {
			rows.Close()
			log.Println(err)
			return snapshot, errors.New("failed to scan row in query results")
		}
		snapshot.wins = append(snapshot.wins, w)
	}
	rows.Close()

	rows, err = query(`
		SELECT USER_ID, CARD_ID, CREATED_ON_DATE, TITLE_CORRECT, ARTIST_CORRECT
		FROM TRACK_TIMELINE_LOG_TITLE_GUESS
		ORDER BY CREATED_ON_DATE ASC, ID ASC
	`)
	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		var g statsGuess
		if err := rows.Scan(&g.UserId, &g.CardId, &g.At, &g.Title, &g.Artist); err != nil {
			rows.Close()
			log.Println(err)
			return snapshot, errors.New("failed to scan row in query results")
		}
		snapshot.guesses = append(snapshot.guesses, g)
	}
	rows.Close()

	rows, err = query("SELECT CARD_ID, EVENT_TYPE FROM TRACK_TIMELINE_LOG_CARD")
	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		var e statsCardEvent
		if err := rows.Scan(&e.CardId, &e.Type); err != nil {
			rows.Close()
			log.Println(err)
			return snapshot, errors.New("failed to scan row in query results")
		}
		snapshot.events = append(snapshot.events, e)
	}
	rows.Close()

	rows, err = query("SELECT ID, NAME, IS_APPROVED FROM USER")
	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		var id uuid.UUID
		var user statsUser
		if err := rows.Scan(&id, &user.Name, &user.Approved); err != nil {
			rows.Close()
			log.Println(err)
			return snapshot, errors.New("failed to scan row in query results")
		}
		snapshot.users[id] = user
	}
	rows.Close()

	// Only songs that appear in a log; a deck of thousands need not be read for
	// the dozens that have actually been played.
	rows, err = query(`
		SELECT C.ID, C.TITLE, C.ARTIST, COALESCE(C.RELEASE_YEAR, 0), COALESCE(G.NAME, '')
		FROM CARD C
			LEFT JOIN TRACK_TIMELINE_CATEGORY G ON G.ID = C.CATEGORY_ID
		WHERE C.ID IN (SELECT CARD_ID FROM TRACK_TIMELINE_LOG_PLACEMENT)
			OR C.ID IN (SELECT CARD_ID FROM TRACK_TIMELINE_LOG_TITLE_GUESS)
			OR C.ID IN (SELECT CARD_ID FROM TRACK_TIMELINE_LOG_CARD)
	`)
	if err != nil {
		return snapshot, err
	}
	for rows.Next() {
		var id uuid.UUID
		var card statsCard
		if err := rows.Scan(&id, &card.Title, &card.Artist, &card.Year, &card.Genre); err != nil {
			rows.Close()
			log.Println(err)
			return snapshot, errors.New("failed to scan row in query results")
		}
		snapshot.cards[id] = card
	}
	rows.Close()

	return snapshot, nil
}

func loadStatsReport() (*StatsReport, error) {
	snapshot, err := loadStatsSnapshot()
	if err != nil {
		return nil, err
	}
	return buildStatsReport(snapshot), nil
}

// --- building ---

// primaryGenre folds a compound genre ("Pop / Rock") to its first part so the
// genre breakdown is not scattered across a dozen near-duplicates.
func primaryGenre(name string) string {
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}
	return strings.TrimSpace(name)
}

func buildStatsReport(s statsSnapshot) *StatsReport {
	report := &StatsReport{users: s.users}

	report.Games = reconstructGames(s.wins, s.placements)
	report.Sessions = buildSessions(s.placements, s.wins)

	// Decade axis: every decade from the oldest to the newest song placed, with
	// no gaps, so every player's row lines up column for column.
	if len(s.placements) > 0 {
		low, high := decadeOf(s.placements[0].Year), decadeOf(s.placements[0].Year)
		for _, p := range s.placements {
			low = min(low, decadeOf(p.Year))
			high = max(high, decadeOf(p.Year))
		}
		for d := low; d <= high; d += 10 {
			report.DecadeAxis = append(report.DecadeAxis, d)
		}
	}
	axisIndex := make(map[int]int, len(report.DecadeAxis))
	for i, d := range report.DecadeAxis {
		axisIndex[d] = i
	}
	report.Eras = make([]EraStat, len(report.DecadeAxis))
	for i, d := range report.DecadeAxis {
		report.Eras[i].Decade = d
	}

	// Players: approved accounts that have placed or guessed.
	players := make(map[uuid.UUID]*PlayerStats)
	playerFor := func(id uuid.UUID) *PlayerStats {
		user, ok := s.users[id]
		if !ok || !user.Approved {
			return nil
		}
		if p, ok := players[id]; ok {
			return p
		}
		p := &PlayerStats{UserId: id, UserName: user.Name, Decades: make([]Rate, len(report.DecadeAxis))}
		players[id] = p
		return p
	}
	playerGenres := make(map[uuid.UUID]map[string]*Rate)
	tableGenres := make(map[string]*Rate)

	songs := make(map[uuid.UUID]*SongStats)
	songFor := func(id uuid.UUID) *SongStats {
		if song, ok := songs[id]; ok {
			return song
		}
		song := &SongStats{CardId: id, Percentile: -1}
		if card, ok := s.cards[id]; ok {
			song.Known = true
			song.Title, song.Artist, song.Year, song.Genre = card.Title, card.Artist, card.Year, card.Genre
		} else {
			song.Title = "(deleted song)"
		}
		songs[id] = song
		return song
	}

	// Placements.
	totals := &report.Totals
	for _, p := range s.placements {
		totals.Placements++
		song := songFor(p.CardId)
		song.Attempts = append(song.Attempts, SongAttempt{UserId: p.UserId, At: p.At, Steal: p.Steal, Correct: p.Correct})
		player := playerFor(p.UserId)
		era := &report.Eras[axisIndex[decadeOf(p.Year)]]

		if p.Steal {
			totals.Steal.tally(p.Correct)
			song.Steal.tally(p.Correct)
			era.Steal.tally(p.Correct)
			if player != nil {
				player.Steal.tally(p.Correct)
			}
			continue
		}

		totals.Turn.tally(p.Correct)
		song.Turn.tally(p.Correct)
		era.Turn.tally(p.Correct)
		if player != nil {
			player.Turn.tally(p.Correct)
			player.Decades[axisIndex[decadeOf(p.Year)]].tally(p.Correct)
		}
		if card, ok := s.cards[p.CardId]; ok {
			if genre := primaryGenre(card.Genre); genre != "" {
				if tableGenres[genre] == nil {
					tableGenres[genre] = &Rate{}
				}
				tableGenres[genre].tally(p.Correct)
				if player != nil {
					if playerGenres[p.UserId] == nil {
						playerGenres[p.UserId] = make(map[string]*Rate)
					}
					if playerGenres[p.UserId][genre] == nil {
						playerGenres[p.UserId][genre] = &Rate{}
					}
					playerGenres[p.UserId][genre].tally(p.Correct)
				}
			}
		}
	}
	totals.Songs = len(songs)
	if len(s.placements) > 0 {
		totals.FirstOn = s.placements[0].At
		totals.LastOn = s.placements[len(s.placements)-1].At
	}
	if totals.Turn.Total > 0 {
		report.TurnAccuracy = float64(totals.Turn.Hit) / float64(totals.Turn.Total)
	} else {
		report.TurnAccuracy = 0.7
	}

	// Streaks.
	var bestId uuid.UUID
	for id, length := range longestStreaks(s.placements) {
		if player := playerFor(id); player != nil {
			player.LongestStreak = length
			if length > report.Records.StreakLength || (length == report.Records.StreakLength && player.UserName < report.Records.StreakPlayerName) {
				report.Records.StreakLength = length
				report.Records.StreakPlayerName = player.UserName
				bestId = id
			}
		}
	}
	report.Records.StreakPlayerId = bestId

	// Guesses.
	for _, g := range s.guesses {
		totals.Guesses++
		totals.Title.tally(g.Title)
		totals.Artist.tally(g.Artist)
		totals.Both.tally(g.Title && g.Artist)

		song := songFor(g.CardId)
		song.Guesses++
		song.NamedTitle.tally(g.Title)
		song.NamedArtist.tally(g.Artist)
		song.Recognised.tally(g.Title)
		song.Recognised.tally(g.Artist)

		if song.Known && song.Year > 0 {
			if i, ok := axisIndex[decadeOf(song.Year)]; ok {
				report.Eras[i].Title.tally(g.Title)
				report.Eras[i].Artist.tally(g.Artist)
			}
		}
		if player := playerFor(g.UserId); player != nil {
			player.Guesses++
			player.Title.tally(g.Title)
			player.Artist.tally(g.Artist)
			player.Both.tally(g.Title && g.Artist)
		}
	}

	// Card lifecycle.
	for _, e := range s.events {
		switch e.Type {
		case CardEventDrawn:
			totals.Draws++
			songFor(e.CardId).Draws++
		case CardEventSkipped:
			totals.Skips++
			songFor(e.CardId).Skips++
		case CardEventBought:
			totals.Bought++
			songFor(e.CardId).Bought++
		case CardEventDiscarded:
			totals.Discards++
			songFor(e.CardId).Discards++
		}
	}

	// Games. Game-length figures only compare games played to the usual card
	// target: a first-to-5 game is not a "short" first-to-10 game.
	targets := make(map[int]int)
	for _, game := range report.Games {
		if !game.Solo() {
			targets[game.CardsToWin]++
		}
	}
	for target, count := range targets {
		if count > targets[totals.StandardCards] || (count == targets[totals.StandardCards] && target > totals.StandardCards) {
			totals.StandardCards = target
		}
	}
	versus := make(map[uuid.UUID]map[uuid.UUID]*VersusRow)
	var durations, sizes []int
	for i := range report.Games {
		game := &report.Games[i]
		winner, winnerKnown := s.users[game.WinnerId]
		game.WinnerKnown = winnerKnown && winner.Approved
		game.WinnerName = report.nameOf(game.WinnerId)

		if game.Solo() {
			totals.SoloGames++
			if p := playerFor(game.WinnerId); p != nil {
				p.SoloWins++
			}
			continue
		}

		totals.Games++
		report.Records.BiggestTable = max(report.Records.BiggestTable, game.PlayerCount)
		if game.CardsToWin == totals.StandardCards {
			durations = append(durations, game.Minutes)
			sizes = append(sizes, game.Placements)
			if report.Records.LongestGame == nil || game.Minutes > report.Records.LongestGame.Minutes {
				report.Records.LongestGame = game
			}
			if report.Records.ShortestGame == nil || game.Minutes < report.Records.ShortestGame.Minutes {
				report.Records.ShortestGame = game
			}
		}

		for _, id := range game.Players {
			player := playerFor(id)
			if player == nil {
				continue
			}
			player.Games++
			player.Expected += 1 / float64(game.PlayerCount)
			won := id == game.WinnerId
			if won {
				player.Wins++
				player.LastWin = game.WonOn
			}
			player.Form = append(player.Form, FormEntry{Won: won, WonOn: game.WonOn, Players: game.PlayerCount})

			for _, other := range game.Players {
				if other == id || playerFor(other) == nil {
					continue
				}
				if versus[id] == nil {
					versus[id] = make(map[uuid.UUID]*VersusRow)
				}
				row := versus[id][other]
				if row == nil {
					row = &VersusRow{OpponentId: other, OpponentName: s.users[other].Name}
					versus[id][other] = row
				}
				row.Games++
				if won {
					row.Wins++
				} else if other == game.WinnerId {
					row.Losses++
				}
			}
		}
	}
	totals.MedianMinutes = median(durations)
	totals.MedianPlaces = median(sizes)
	totals.Sessions = len(report.Sessions)

	// Players: finish and order.
	for id, p := range players {
		if p.Turn.Total == 0 && p.Steal.Total == 0 && p.Guesses == 0 {
			continue
		}
		if len(p.Form) > 8 {
			p.Form = p.Form[len(p.Form)-8:]
		}
		for genre, rate := range playerGenres[id] {
			if rate.Total >= 5 {
				p.Genres = append(p.Genres, NamedRate{Name: genre, Rate: *rate})
			}
		}
		sort.Slice(p.Genres, func(a, b int) bool {
			if p.Genres[a].Rate.Total != p.Genres[b].Rate.Total {
				return p.Genres[a].Rate.Total > p.Genres[b].Rate.Total
			}
			return p.Genres[a].Name < p.Genres[b].Name
		})
		if len(p.Genres) > 6 {
			p.Genres = p.Genres[:6]
		}
		for _, row := range versus[id] {
			p.Versus = append(p.Versus, *row)
		}
		sort.Slice(p.Versus, func(a, b int) bool {
			if p.Versus[a].Games != p.Versus[b].Games {
				return p.Versus[a].Games > p.Versus[b].Games
			}
			return p.Versus[a].OpponentName < p.Versus[b].OpponentName
		})
		report.Players = append(report.Players, *p)
	}
	sort.Slice(report.Players, func(a, b int) bool {
		return strings.ToLower(report.Players[a].UserName) < strings.ToLower(report.Players[b].UserName)
	})
	totals.Players = len(report.Players)

	// Leaderboard ranks: competition ranking on (wins, win rate), so players
	// who are level share a position.
	for i := range report.Players {
		p := &report.Players[i]
		if p.Games == 0 {
			continue
		}
		p.Rank = 1
		for j := range report.Players {
			if j != i && report.Players[j].Games > 0 && leaderboardBefore(report.Players[j], *p) {
				p.Rank++
			}
		}
	}

	// Table-wide genres.
	for genre, rate := range tableGenres {
		if rate.Total >= 10 {
			report.Genres = append(report.Genres, NamedRate{Name: genre, Rate: *rate})
		}
	}
	sort.Slice(report.Genres, func(a, b int) bool {
		if report.Genres[a].Rate.Total != report.Genres[b].Rate.Total {
			return report.Genres[a].Rate.Total > report.Genres[b].Rate.Total
		}
		return report.Genres[a].Name < report.Genres[b].Name
	})
	if len(report.Genres) > 8 {
		report.Genres = report.Genres[:8]
	}

	// Songs: rank difficulty among those with enough tries, and attach names to
	// each attempt now that every player is known.
	rankable := make([]float64, 0, len(songs))
	for _, song := range songs {
		song.Smoothed = song.Turn.Smoothed(report.TurnAccuracy, smoothingStrength)
		if song.Turn.Total >= minSongTries {
			rankable = append(rankable, song.Smoothed)
		}
		for i := range song.Attempts {
			song.Attempts[i].UserName = report.nameOf(song.Attempts[i].UserId)
		}
	}
	for _, song := range songs {
		if song.Turn.Total >= minSongTries && len(rankable) > 0 {
			easier := 0
			for _, v := range rankable {
				if v > song.Smoothed {
					easier++
				}
			}
			song.Percentile = 100 * easier / len(rankable)
		}
		report.Songs = append(report.Songs, *song)
	}
	sort.Slice(report.Songs, func(a, b int) bool { return report.Songs[a].Title < report.Songs[b].Title })

	return report
}

// leaderboardBefore reports whether a ranks strictly above b.
func leaderboardBefore(a, b PlayerStats) bool {
	if a.Wins != b.Wins {
		return a.Wins > b.Wins
	}
	// Cross-multiplied to compare win rates exactly: a.Wins/a.Games vs b.Wins/b.Games.
	return a.Wins*b.Games > b.Wins*a.Games
}

// --- page-level getters ---

// StatsOverview is the Overview tab.
type StatsOverview struct {
	Totals     StatsTotals
	Records    StatsRecords
	Sessions   []StatsSession // most recent first
	Recent     []StatsGame    // most recent first
	AxisLabels []string
	Eras       []EraStat
	Genres     []NamedRate
	Hardest    []SongStats
	Unknown    []SongStats
	Skipped    []SongStats
	MinTries   int
	Empty      bool
}

// GetStatsOverview assembles the Overview tab.
func GetStatsOverview() (StatsOverview, error) {
	report, err := loadStatsReport()
	if err != nil {
		return StatsOverview{}, err
	}

	overview := StatsOverview{
		Totals:     report.Totals,
		Records:    report.Records,
		AxisLabels: report.AxisLabels(),
		Eras:       report.Eras,
		Genres:     report.Genres,
		MinTries:   minSongTries,
		Empty:      report.Totals.Placements == 0 && len(report.Games) == 0,
	}
	for i := len(report.Sessions) - 1; i >= 0 && len(overview.Sessions) < 6; i-- {
		overview.Sessions = append(overview.Sessions, report.Sessions[i])
	}
	for i := len(report.Games) - 1; i >= 0 && len(overview.Recent) < 8; i-- {
		overview.Recent = append(overview.Recent, report.Games[i])
	}
	overview.Hardest = limitSongs(songView(report, SongViewHardest), 3)
	overview.Unknown = limitSongs(songView(report, SongViewUnknown), 3)
	overview.Skipped = limitSongs(songView(report, SongViewSkipped), 3)
	return overview, nil
}

// Leaderboard is the Leaderboard tab.
type Leaderboard struct {
	Rows         []PlayerStats // best first
	TotalGames   int
	SoloGames    int
	NotYetRanked []PlayerStats // players with no finished game against an opponent
}

// GetLeaderboard ranks every approved player who has finished a game against at
// least one opponent: most wins first, then the better win rate. Wins in solo
// games are not counted — nobody was beaten.
func GetLeaderboard() (Leaderboard, error) {
	report, err := loadStatsReport()
	if err != nil {
		return Leaderboard{}, err
	}

	board := Leaderboard{TotalGames: report.Totals.Games, SoloGames: report.Totals.SoloGames}
	for _, p := range report.Players {
		if p.Games > 0 {
			board.Rows = append(board.Rows, p)
		} else {
			board.NotYetRanked = append(board.NotYetRanked, p)
		}
	}
	sort.SliceStable(board.Rows, func(a, b int) bool {
		if board.Rows[a].Rank != board.Rows[b].Rank {
			return board.Rows[a].Rank < board.Rows[b].Rank
		}
		return strings.ToLower(board.Rows[a].UserName) < strings.ToLower(board.Rows[b].UserName)
	})
	return board, nil
}

// HeatCell is one cell of the player-by-era grid: a player's own-turn rate for
// a decade, judged against everyone's rate for that same decade. Judging against
// the decade matters because some eras are harder for the whole table; shading
// absolute accuracy would mostly just redraw that.
type HeatCell struct {
	Rate
	Base Rate
}

// heatFullTint is the gap, in percentage points, at which a cell reaches its
// strongest shading.
const heatFullTint = 25.0

// Class is "stat-heat-up" or "stat-heat-down" when the cell is at least three
// points off the table with enough tries to mean it, else empty (unshaded).
func (c HeatCell) Class() string {
	switch c.Rate.DeltaClass(c.Base) {
	case "stat-pos":
		return "stat-heat-up"
	case "stat-neg":
		return "stat-heat-down"
	}
	return ""
}

// Tint is the shading strength, 0-100, growing with the size of the gap.
func (c HeatCell) Tint() int {
	if c.Class() == "" {
		return 0
	}
	return int(math.Round(100 * math.Min(math.Abs(c.Rate.Delta(c.Base))/heatFullTint, 1)))
}

// Sub is the cell's small second line: the gap in points (when there is enough
// data to state one) and the number of placements behind the cell.
func (c HeatCell) Sub() string {
	if gap := c.Rate.DeltaLabel(c.Base); gap != "" {
		return fmt.Sprintf("%s \u00b7 %d", gap, c.Total)
	}
	return fmt.Sprintf("%d", c.Total)
}

// Tip is the tooltip: the counts and range, plus how it compares with the table.
func (c HeatCell) Tip() string {
	tip := c.Rate.Tip()
	if c.Base.Has() {
		tip += fmt.Sprintf(" \u00b7 table %s", c.Base.Percent())
	}
	return tip
}

// PlayerRow is a player with their era cells.
type PlayerRow struct {
	PlayerStats
	Cells []HeatCell
}

// PlayerList is the Players tab.
type PlayerList struct {
	Rows       []PlayerRow
	AxisLabels []string
	Everyone   []HeatCell // the table-wide row under the per-player era grid
}

// GetPlayerList returns every approved player with any recorded activity.
func GetPlayerList() (PlayerList, error) {
	report, err := loadStatsReport()
	if err != nil {
		return PlayerList{}, err
	}

	list := PlayerList{AxisLabels: report.AxisLabels()}
	for _, era := range report.Eras {
		list.Everyone = append(list.Everyone, HeatCell{Rate: era.Turn})
	}
	for _, player := range report.Players {
		row := PlayerRow{PlayerStats: player}
		for i, rate := range player.Decades {
			row.Cells = append(row.Cells, HeatCell{Rate: rate, Base: report.Eras[i].Turn})
		}
		list.Rows = append(list.Rows, row)
	}
	return list, nil
}

// PlayerPage is one player's detail page.
type PlayerPage struct {
	Player     PlayerStats
	AxisLabels []string
	Table      StatsTotals
	TableEras  []EraStat
}

// GetPlayerStats returns one player's record; found is false for an unknown or
// unapproved account (room guests have no stats page).
func GetPlayerStats(userId uuid.UUID) (PlayerPage, bool, error) {
	report, err := loadStatsReport()
	if err != nil {
		return PlayerPage{}, false, err
	}
	for _, p := range report.Players {
		if p.UserId == userId {
			return PlayerPage{Player: p, AxisLabels: report.AxisLabels(), Table: report.Totals, TableEras: report.Eras}, true, nil
		}
	}
	return PlayerPage{}, false, nil
}

// SongRow is a song in a ranked list.
type SongRow struct {
	Rank int
	SongStats
}

// SongList is the Songs tab.
type SongList struct {
	View     string
	Rows     []SongRow
	Total    int // songs the view considered before the cut-off
	MinTries int
}

func limitSongs(songs []SongStats, n int) []SongStats {
	if len(songs) > n {
		return songs[:n]
	}
	return songs
}

// songView filters and orders the report's songs for one Songs-tab view.
// Deleted songs are never listed (they have no name to show); their stats
// remain reachable by id.
func songView(report *StatsReport, view string) []SongStats {
	songs := make([]SongStats, 0)
	for _, song := range report.Songs {
		if !song.Known {
			continue
		}
		switch view {
		case SongViewHardest, SongViewEasiest:
			if song.Turn.Total >= minSongTries {
				songs = append(songs, song)
			}
		case SongViewUnknown:
			if song.Guesses >= minGuessesForRecognition {
				songs = append(songs, song)
			}
		case SongViewSkipped:
			if song.Skips > 0 {
				songs = append(songs, song)
			}
		default:
			if song.Turn.Total+song.Steal.Total > 0 {
				songs = append(songs, song)
			}
		}
	}

	recognised := func(s SongStats) float64 {
		return s.Recognised.Smoothed(report.Totals.recognitionPrior(), smoothingStrength)
	}
	sort.SliceStable(songs, func(a, b int) bool {
		x, y := songs[a], songs[b]
		switch view {
		case SongViewHardest:
			if x.Smoothed != y.Smoothed {
				return x.Smoothed < y.Smoothed
			}
		case SongViewEasiest:
			if x.Smoothed != y.Smoothed {
				return x.Smoothed > y.Smoothed
			}
		case SongViewUnknown:
			if recognised(x) != recognised(y) {
				return recognised(x) < recognised(y)
			}
		case SongViewSkipped:
			if x.Skips != y.Skips {
				return x.Skips > y.Skips
			}
			if x.Draws != y.Draws {
				return x.Draws < y.Draws
			}
		default:
			if xt, yt := x.Turn.Total+x.Steal.Total, y.Turn.Total+y.Steal.Total; xt != yt {
				return xt > yt
			}
		}
		if x.Turn.Total != y.Turn.Total {
			return x.Turn.Total > y.Turn.Total
		}
		return x.Title < y.Title
	})
	return songs
}

// recognitionPrior is the table-wide rate at which a title or artist is named.
func (t StatsTotals) recognitionPrior() float64 {
	pooled := t.Title.add(t.Artist)
	if pooled.Total == 0 {
		return 0.5
	}
	return float64(pooled.Hit) / float64(pooled.Total)
}

// GetSongList returns one Songs-tab view, capped at 50 rows.
func GetSongList(view string) (SongList, error) {
	report, err := loadStatsReport()
	if err != nil {
		return SongList{}, err
	}
	switch view {
	case SongViewEasiest, SongViewPlayed, SongViewUnknown, SongViewSkipped:
	default:
		view = SongViewHardest
	}
	songs := songView(report, view)
	list := SongList{View: view, Total: len(songs), MinTries: minSongTries}
	for i, song := range limitSongs(songs, 50) {
		list.Rows = append(list.Rows, SongRow{Rank: i + 1, SongStats: song})
	}
	return list, nil
}

// SongPage is one song's detail page.
type SongPage struct {
	Song     SongStats
	Table    StatsTotals
	Rankable int // songs with enough tries to be ranked, for "harder than X of Y"
	MinTries int
}

// GetSongStats returns one song's record. found is false only when the id has
// never appeared in any log; a deleted song still resolves, as "(deleted song)".
func GetSongStats(cardId uuid.UUID) (SongPage, bool, error) {
	report, err := loadStatsReport()
	if err != nil {
		return SongPage{}, false, err
	}

	page := SongPage{Table: report.Totals, MinTries: minSongTries}
	for _, song := range report.Songs {
		if song.Turn.Total >= minSongTries {
			page.Rankable++
		}
		if song.CardId == cardId {
			page.Song = song
		}
	}
	if page.Song.CardId == uuid.Nil {
		// A known card nobody has played yet still deserves a page.
		card, err := GetCard(cardId)
		if err != nil || card.Id == uuid.Nil {
			return page, false, err
		}
		page.Song = SongStats{CardId: cardId, Known: true, Title: card.Title, Artist: card.Artist, Percentile: -1}
		if card.ReleaseYear.Valid {
			page.Song.Year = int(card.ReleaseYear.Int64)
		}
	}

	history, err := loadSongGuesses(cardId, report)
	if err != nil {
		return page, false, err
	}
	page.Song.History = history

	// Newest first, the order a reader wants a history in.
	attempts := make([]SongAttempt, len(page.Song.Attempts))
	for i, attempt := range page.Song.Attempts {
		attempts[len(attempts)-1-i] = attempt
	}
	page.Song.Attempts = attempts
	return page, true, nil
}

// loadSongGuesses reads what players typed about one song, newest first. The
// text is only fetched for the song being viewed; the report itself never
// carries it.
func loadSongGuesses(cardId uuid.UUID, report *StatsReport) ([]SongGuess, error) {
	rows, err := query(`
		SELECT USER_ID, CREATED_ON_DATE, GUESS_TEXT, TITLE_CORRECT, ARTIST_CORRECT
		FROM TRACK_TIMELINE_LOG_TITLE_GUESS
		WHERE CARD_ID = ?
		ORDER BY CREATED_ON_DATE DESC
		LIMIT 25
	`, cardId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	guesses := make([]SongGuess, 0)
	for rows.Next() {
		var userId uuid.UUID
		var g SongGuess
		if err := rows.Scan(&userId, &g.At, &g.Text, &g.Title, &g.Artist); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		g.UserName = report.nameOf(userId)
		guesses = append(guesses, g)
	}
	return guesses, nil
}
