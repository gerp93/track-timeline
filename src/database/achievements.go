package database

import (
	"fmt"

	"github.com/google/uuid"
)

// Achievements are unlocked for doing things in the game. Each one is a metric
// (a number computed for a player from the logs, see achievements-metrics.go)
// and a ladder of thresholds: reach the first and tier 1 is unlocked, the next
// and tier 2 is, and so on. A single achievement is simply a ladder with one
// rung. Everything the player sees about an achievement (its name, what it asks
// for, how far along they are) comes from the catalog below, so adding one is
// adding an entry here and, if no existing metric measures it, a metric.
//
// The metrics are derived from the same append-only logs the statistics pages
// read, plus TRACK_TIMELINE_LOG_ACHIEVEMENT_EVENT for the moments no other log
// keeps. Nothing about progress is stored; only the unlocks are.

// Achievement categories, in the order the trophy case lists them.
const (
	CategoryWagering   = "Wagering"
	CategoryGames      = "Games & wins"
	CategoryStreaks    = "Streaks"
	CategoryEconomy    = "Steals & tokens"
	CategoryGuessing   = "Guessing"
	CategoryAldean     = "Jason Aldean"
	CategoryEras       = "Eras & genres"
	CategoryChallenges = "Challenges"
	CategoryHosting    = "Hosting"
)

// AchievementCategories is the display order of the categories.
var AchievementCategories = []string{
	CategoryWagering,
	CategoryGames,
	CategoryStreaks,
	CategoryEconomy,
	CategoryGuessing,
	CategoryAldean,
	CategoryEras,
	CategoryChallenges,
	CategoryHosting,
}

// tierLabels name the rungs of a ladder with more than one. A single
// achievement carries no label.
var tierLabels = []string{"Bronze", "Silver", "Gold", "Platinum", "Diamond", "Master"}

// Metric keys. A metric is a count unless its comment says otherwise.
const (
	MetricWins           = "wins"           // finished games won at a table of two or more
	MetricGames          = "games"          // finished games played at a table of two or more
	MetricWinStreak      = "win_streak"     // most finished games won in a row, among the games they played
	MetricSessionGames   = "session_games"  // most games played in one sitting
	MetricTableWin       = "table_win"      // biggest table they have won
	MetricWorlds         = "worlds"         // 0-2: whether they have won online, and in person
	MetricBestStreak     = "best_streak"    // longest run of correct own-turn placements
	MetricMissStreak     = "miss_streak"    // longest run of missed own-turn placements
	MetricBounceBack     = "bounce_back"    // hits that came straight after three or more misses
	MetricStealsWon      = "steals_won"     // steals that took the card
	MetricBought         = "bought"         // cards bought with tokens
	MetricBoth           = "both"           // guesses with the title and the artist both right
	MetricBothStreak     = "both_streak"    // most such guesses in a row
	MetricArtistOnly     = "artist_only"    // guesses with the artist right and the title wrong
	MetricJasonTotal     = "jason_total"    // times "easy, that's Jason Aldean" was ticked
	MetricJasonRight     = "jason_right"    // ... and the artist really was Jason Aldean
	MetricJasonWrong     = "jason_wrong"    // ... and it was not
	MetricDecades15      = "decades_15"     // decades with 15 or more correct own-turn placements
	MetricGenres         = "genres"         // distinct genres with a correct own-turn placement
	MetricWagers         = "wagers"         // exact-year wagers placed
	MetricWagerHits      = "wager_hits"     // ... that were right
	MetricWagerStreak    = "wager_streak"   // most wagers right in a row
	MetricWagerBigWin    = "wager_big_win"  // biggest stake on a wager that came in
	MetricWagerBigLoss   = "wager_big_loss" // biggest stake on a wager that missed
	MetricWagerNetUp     = "wager_net_up"   // tokens won on wagers, net, when positive
	MetricWagerNetDown   = "wager_net_down" // tokens lost on wagers, net, when negative
	metricDecadePrefix   = "decade_"        // decade_1980: correct own-turn placements from the 1980s
	metricEventMaxSuffix = "_max"           // an event's biggest VALUE
	metricEventMinSuffix = "_min"           // an event's smallest VALUE
)

// DecadeMetric is the metric key for correct own-turn placements in a decade.
func DecadeMetric(decade int) string { return fmt.Sprintf("%s%d", metricDecadePrefix, decade) }

// Achievement is one entry in the catalog.
type Achievement struct {
	Key      string
	Name     string
	Icon     string // a Bootstrap Icons class suffix: "trophy" for bi-trophy
	Category string
	// Metric is the key of the number the tiers are measured against.
	Metric string
	// Tiers are the thresholds, easiest first. For a Lower achievement the
	// metric is a best time and each tier asks for no more than its threshold.
	Tiers []int
	Lower bool
	// Seconds says the metric and thresholds are in seconds and read in minutes.
	Seconds bool
	// Secret achievements stay out of the trophy case until a tier is unlocked.
	Secret bool
	// Describe says what a tier asks for, in a sentence without a full stop.
	Describe func(threshold int) string
}

// count phrases n of a thing: "1 win", "3 wins".
func count(n int, one string, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// decadeDescribe is the description shared by every decade ladder.
func decadeDescribe(decade int) func(int) string {
	return func(n int) string {
		return fmt.Sprintf("Place %s from the %ds correctly on your turn", count(n, "song", "songs"), decade)
	}
}

// decadeAchievement builds the ladder for one decade.
func decadeAchievement(decade int, name string, icon string, tiers ...int) Achievement {
	return Achievement{
		Key:      fmt.Sprintf("decade_%d", decade),
		Name:     name,
		Icon:     icon,
		Category: CategoryEras,
		Metric:   DecadeMetric(decade),
		Tiers:    tiers,
		Describe: decadeDescribe(decade),
	}
}

// Achievements is the catalog, in the order the trophy case lists each category.
// Thresholds were set against the first seven weeks of real play (15 games, four
// players): a rung is meant to be reachable by someone who plays regularly, with
// the top rungs a long haul away or never yet reached.
var Achievements = []Achievement{
	// ---- Wagering
	{
		Key: "nostradamus", Name: "Nostradamus", Icon: "eye", Category: CategoryWagering,
		Metric: MetricWagerHits, Tiers: []int{1, 3, 10, 25},
		Describe: func(n int) string { return "Nail the exact year on " + count(n, "wager", "wagers") },
	},
	{
		Key: "gambler", Name: "Gambler", Icon: "dice-5", Category: CategoryWagering,
		Metric: MetricWagers, Tiers: []int{1, 5, 15, 40},
		Describe: func(n int) string { return "Place " + count(n, "exact-year wager", "exact-year wagers") },
	},
	{
		Key: "sharpshooter", Name: "Sharpshooter", Icon: "bullseye", Category: CategoryWagering,
		Metric: MetricWagerStreak, Tiers: []int{2, 3, 5},
		Describe: func(n int) string { return fmt.Sprintf("Hit %d exact-year wagers in a row", n) },
	},
	{
		Key: "high_roller", Name: "High Roller", Icon: "cash-stack", Category: CategoryWagering,
		Metric: MetricWagerBigWin, Tiers: []int{10, 15, 25},
		Describe: func(n int) string { return fmt.Sprintf("Hit an exact-year wager with a stake of %d tokens or more", n) },
	},
	{
		Key: "sunk_cost", Name: "Sunk Cost", Icon: "graph-down-arrow", Category: CategoryWagering,
		Metric: MetricWagerBigLoss, Tiers: []int{10, 15, 25},
		Describe: func(n int) string {
			return fmt.Sprintf("Miss an exact-year wager with a stake of %d tokens or more", n)
		},
	},
	{
		Key: "wolf_of_wall_street", Name: "Wolf of Wall Street", Icon: "graph-up-arrow", Category: CategoryWagering,
		Metric: MetricWagerNetUp, Tiers: []int{20, 60, 150},
		Describe: func(n int) string { return fmt.Sprintf("Be up %d tokens overall on exact-year wagers", n) },
	},
	{
		Key: "house_always_wins", Name: "The House Always Wins", Icon: "bank", Category: CategoryWagering, Secret: true,
		Metric: MetricWagerNetDown, Tiers: []int{30, 75, 150},
		Describe: func(n int) string { return fmt.Sprintf("Be down %d tokens overall on exact-year wagers", n) },
	},
	{
		Key: "all_in", Name: "All In", Icon: "gem", Category: CategoryWagering,
		Metric: EvAllIn, Tiers: []int{1},
		Describe: func(int) string {
			return fmt.Sprintf("Wager your whole stash (%d tokens or more) on an exact year, and hit", AllInMinStake)
		},
	},
	{
		Key: "rock_bottom", Name: "Rock Bottom", Icon: "box-arrow-down", Category: CategoryWagering, Secret: true,
		Metric: EvRockBottom, Tiers: []int{1},
		Describe: func(int) string {
			return fmt.Sprintf("Wager everything you have (%d tokens or more) on an exact year, and miss", RockBottomMinStake)
		},
	},
	{
		Key: "triple_crown", Name: "Triple Crown", Icon: "award", Category: CategoryWagering,
		Metric: EvTripleCrown, Tiers: []int{1},
		Describe: func(int) string { return "Name the exact year, the artist and the title of the same song" },
	},

	// ---- Games & wins
	{
		Key: "dynasty", Name: "Dynasty", Icon: "trophy", Category: CategoryGames,
		Metric: MetricWins, Tiers: []int{1, 3, 10, 25, 50},
		Describe: func(n int) string { return "Win " + count(n, "game", "games") },
	},
	{
		Key: "regular", Name: "Regular", Icon: "calendar-check", Category: CategoryGames,
		Metric: MetricGames, Tiers: []int{1, 10, 25, 50, 100},
		Describe: func(n int) string { return "Play " + count(n, "finished game", "finished games") },
	},
	{
		Key: "win_streak", Name: "Win Streak", Icon: "lightning-charge", Category: CategoryGames,
		Metric: MetricWinStreak, Tiers: []int{2, 3, 5},
		Describe: func(n int) string { return fmt.Sprintf("Win %d games in a row", n) },
	},
	{
		Key: "marathon_night", Name: "Marathon Night", Icon: "moon-stars", Category: CategoryGames,
		Metric: MetricSessionGames, Tiers: []int{3, 4, 5},
		Describe: func(n int) string { return fmt.Sprintf("Play %d games in one sitting", n) },
	},
	{
		Key: "time_lord", Name: "Time Lord", Icon: "hourglass-split", Category: CategoryGames,
		Metric: EvWinSpan + metricEventMaxSuffix, Tiers: []int{55, 60, 65},
		Describe: func(n int) string { return fmt.Sprintf("Win with a timeline spanning %d years or more", n) },
	},
	{
		Key: "full_house", Name: "Full House", Icon: "people-fill", Category: CategoryGames,
		Metric: MetricTableWin, Tiers: []int{5, 6, 8},
		Describe: func(n int) string { return fmt.Sprintf("Win a game at a table of %d or more", n) },
	},
	{
		Key: "flawless_victory", Name: "Flawless Victory", Icon: "patch-check", Category: CategoryGames,
		Metric: EvFlawless, Tiers: []int{1},
		Describe: func(int) string { return "Win a game without missing a single placement on your own turn" },
	},
	{
		Key: "comeback_kid", Name: "Comeback Kid", Icon: "arrow-counterclockwise", Category: CategoryGames,
		Metric: EvComeback, Tiers: []int{1},
		Describe: func(int) string {
			return fmt.Sprintf("Win a game after trailing the leader by %d or more cards", ComebackDeficit)
		},
	},
	{
		Key: "photo_finish", Name: "Photo Finish", Icon: "camera", Category: CategoryGames,
		Metric: EvPhotoFinish, Tiers: []int{1},
		Describe: func(int) string { return "Win a game while a rival is exactly one card from winning" },
	},
	{
		Key: "the_heist", Name: "The Heist", Icon: "incognito", Category: CategoryGames,
		Metric: EvHeist, Tiers: []int{1},
		Describe: func(int) string { return "Win the game by stealing the final card" },
	},
	{
		Key: "both_worlds", Name: "Both Worlds", Icon: "globe2", Category: CategoryGames,
		Metric: MetricWorlds, Tiers: []int{2},
		Describe: func(int) string { return "Win one online game and one in-person game" },
	},
	{
		Key: "quick_draw", Name: "Quick Draw", Icon: "stopwatch", Category: CategoryGames,
		Metric: EvWinSeconds + metricEventMinSuffix, Tiers: []int{25 * 60, 20 * 60}, Lower: true, Seconds: true,
		Describe: func(n int) string {
			return fmt.Sprintf("Win a %d-card game in %d minutes or less", StandardCardsToWin, n/60)
		},
	},
	{
		Key: "lightning", Name: "Lightning", Icon: "lightning", Category: CategoryGames, Secret: true,
		Metric: EvWinSeconds + metricEventMinSuffix, Tiers: []int{15 * 60}, Lower: true, Seconds: true,
		Describe: func(n int) string {
			return fmt.Sprintf("Win a %d-card game in %d minutes or less", StandardCardsToWin, n/60)
		},
	},
	{
		Key: "long_haul", Name: "Long Haul", Icon: "hourglass-bottom", Category: CategoryGames,
		Metric: EvWinSeconds + metricEventMaxSuffix, Tiers: []int{40 * 60, 50 * 60}, Seconds: true,
		Describe: func(n int) string {
			return fmt.Sprintf("Win a %d-card game that lasts %d minutes or more", StandardCardsToWin, n/60)
		},
	},
	{
		Key: "endurance", Name: "Endurance", Icon: "hourglass-split", Category: CategoryGames, Secret: true,
		Metric: EvWinSeconds + metricEventMaxSuffix, Tiers: []int{60 * 60}, Seconds: true,
		Describe: func(n int) string {
			return fmt.Sprintf("Win a %d-card game that lasts an hour or more", StandardCardsToWin)
		},
	},

	// ---- Streaks
	{
		Key: "on_fire", Name: "On Fire", Icon: "fire", Category: CategoryStreaks,
		Metric: MetricBestStreak, Tiers: []int{5, 8, 12, 20},
		Describe: func(n int) string { return fmt.Sprintf("Place %d songs correctly in a row on your turn", n) },
	},
	{
		Key: "cold_snap", Name: "Cold Snap", Icon: "snow", Category: CategoryStreaks, Secret: true,
		Metric: MetricMissStreak, Tiers: []int{5},
		Describe: func(n int) string { return fmt.Sprintf("Miss %d placements in a row on your turn", n) },
	},
	{
		Key: "bounce_back", Name: "Bounce Back", Icon: "arrow-up-circle", Category: CategoryStreaks,
		Metric: MetricBounceBack, Tiers: []int{1},
		Describe: func(int) string { return "Land a correct placement straight after missing three or more in a row" },
	},

	// ---- Steals & tokens
	{
		Key: "highway_robber", Name: "Highway Robber", Icon: "key", Category: CategoryEconomy,
		Metric: MetricStealsWon, Tiers: []int{1, 5, 15, 40},
		Describe: func(n int) string { return "Steal " + count(n, "card", "cards") },
	},
	{
		Key: "easy_mark", Name: "Easy Mark", Icon: "emoji-frown", Category: CategoryEconomy,
		Metric: EvStolenFrom, Tiers: []int{1, 5, 15, 40},
		Describe: func(n int) string { return "Have " + count(n, "card", "cards") + " stolen out from under you" },
	},
	{
		Key: "tycoon", Name: "Tycoon", Icon: "coin", Category: CategoryEconomy,
		Metric: MetricBought, Tiers: []int{1, 5, 15, 40},
		Describe: func(n int) string { return "Buy " + count(n, "card", "cards") + " with tokens" },
	},
	{
		Key: "dragon_hoard", Name: "Dragon Hoard", Icon: "piggy-bank", Category: CategoryEconomy,
		Metric: EvPeakTokens + metricEventMaxSuffix, Tiers: []int{20, 30, 45},
		Describe: func(n int) string { return fmt.Sprintf("Hold %d tokens at the end of a round", n) },
	},
	{
		Key: "fort_knox", Name: "Fort Knox", Icon: "shield-lock", Category: CategoryEconomy,
		Metric: EvFortKnox, Tiers: []int{1},
		Describe: func(int) string {
			return fmt.Sprintf("Win a game after fending off %d or more steals and having nothing stolen", FortKnoxDefences)
		},
	},
	{
		Key: "flat_broke", Name: "Flat Broke", Icon: "wallet2", Category: CategoryEconomy, Secret: true,
		Metric: EvFlatBroke, Tiers: []int{1},
		Describe: func(int) string {
			return fmt.Sprintf("Go from %d or more tokens to none in one game", FlatBrokePeak)
		},
	},
	{
		Key: "nothing_to_lose", Name: "Nothing to Lose", Icon: "exclamation-triangle", Category: CategoryEconomy,
		Metric: EvNothingToLose, Tiers: []int{1},
		Describe: func(int) string { return "Spend your last tokens on a steal" },
	},

	// ---- Guessing
	{
		Key: "encyclopedia", Name: "Encyclopedia", Icon: "book", Category: CategoryGuessing,
		Metric: MetricBoth, Tiers: []int{1, 10, 50, 150, 500},
		Describe: func(n int) string { return "Name both the title and the artist on " + count(n, "song", "songs") },
	},
	{
		Key: "double_dip", Name: "Double Dip", Icon: "check2-all", Category: CategoryGuessing,
		Metric: EvDoubleDip, Tiers: []int{1, 10, 40, 100},
		Describe: func(n int) string {
			return "Place a song correctly and name its title and artist, on " + count(n, "turn", "turns")
		},
	},
	{
		Key: "jukebox_savant", Name: "Jukebox Savant", Icon: "vinyl", Category: CategoryGuessing,
		Metric: MetricBothStreak, Tiers: []int{4, 7, 10, 15},
		Describe: func(n int) string { return fmt.Sprintf("Name both the title and the artist on %d songs in a row", n) },
	},
	{
		Key: "hit_and_miss", Name: "Hit and Miss", Icon: "mic", Category: CategoryGuessing,
		Metric: MetricArtistOnly, Tiers: []int{25, 75, 200},
		Describe: func(n int) string { return fmt.Sprintf("Name the artist but not the title, %d times", n) },
	},
	{
		Key: "spelling_beent", Name: "Spelling Bee-n't", Icon: "spellcheck", Category: CategoryGuessing,
		Metric: EvSpellingForgiven, Tiers: []int{1, 10, 50},
		Describe: func(n int) string {
			return "Get credit for " + count(n, "guess", "guesses") + " despite misspelling it"
		},
	},

	// ---- Jason Aldean
	{
		Key: "easy_aldean", Name: "Easy, That's Jason Aldean", Icon: "emoji-sunglasses", Category: CategoryAldean,
		Metric: MetricJasonTotal, Tiers: []int{1},
		Describe: func(int) string { return "Tick “easy, that's Jason Aldean” for the first time" },
	},
	{
		Key: "aldean_devotee", Name: "Aldean Devotee", Icon: "heart", Category: CategoryAldean,
		Metric: MetricJasonTotal, Tiers: []int{5, 15, 30, 60, 120, 250},
		Describe: func(n int) string { return fmt.Sprintf("Tick “easy, that's Jason Aldean” %d times", n) },
	},
	{
		Key: "wrong_aldean", Name: "Wrong Aldean", Icon: "x-circle", Category: CategoryAldean,
		Metric: MetricJasonWrong, Tiers: []int{3, 10, 25, 50, 100},
		Describe: func(n int) string {
			return fmt.Sprintf("Tick “easy, that's Jason Aldean” on a song that isn't his, %d times", n)
		},
	},
	{
		Key: "certified_aldean", Name: "Certified Aldean", Icon: "patch-check-fill", Category: CategoryAldean,
		Metric: MetricJasonRight, Tiers: []int{1, 3, 6, 10},
		Describe: func(n int) string {
			return "Tick “easy, that's Jason Aldean” on a song that really is his, " + count(n, "time", "times")
		},
	},

	// ---- Eras & genres
	decadeAchievement(1950, "Poodle Skirt", "music-note-beamed", 1, 3, 8),
	decadeAchievement(1960, "Flower Power", "flower1", 5, 15, 40, 80),
	decadeAchievement(1970, "Bell Bottoms", "disc", 10, 30, 75, 150),
	decadeAchievement(1980, "Big Hair", "lightning-charge-fill", 10, 25, 60, 120),
	decadeAchievement(1990, "Dial-Up", "tv", 10, 25, 60, 120),
	decadeAchievement(2000, "Flip Phone", "phone", 10, 25, 60, 120),
	decadeAchievement(2010, "Hashtag", "hash", 10, 25, 60, 120),
	decadeAchievement(2020, "Fresh Off the Press", "newspaper", 1, 3, 8),
	{
		Key: "time_traveler", Name: "Time Traveler", Icon: "compass", Category: CategoryEras,
		Metric: MetricDecades15, Tiers: []int{2, 4, 6},
		Describe: func(n int) string {
			return fmt.Sprintf("Have %d or more correct placements in each of %d different decades", TimeTravelerPerDecade, n)
		},
	},
	{
		Key: "renaissance", Name: "Renaissance", Icon: "palette", Category: CategoryEras,
		Metric: MetricGenres, Tiers: []int{10, 15, 20, 25},
		Describe: func(n int) string { return fmt.Sprintf("Place a song correctly in %d different genres", n) },
	},
	{
		Key: "needle_in_a_haystack", Name: "Needle in a Haystack", Icon: "search", Category: CategoryEras,
		Metric: EvNeedle, Tiers: []int{1},
		Describe: func(int) string { return "Land a correct placement in a gap of a year or less between two neighbours" },
	},
	{
		Key: "so_close", Name: "So Close", Icon: "rulers", Category: CategoryEras,
		Metric: EvSoClose, Tiers: []int{1, 5, 15},
		Describe: func(n int) string { return "Miss a placement by exactly one year, " + count(n, "time", "times") },
	},

	// ---- Challenges
	{
		Key: "objection", Name: "Objection!", Icon: "megaphone", Category: CategoryChallenges,
		Metric: EvChallengeRaised, Tiers: []int{1, 5, 15, 40},
		Describe: func(n int) string { return "Raise " + count(n, "challenge", "challenges") },
	},
	{
		Key: "lawyered_up", Name: "Lawyered Up", Icon: "hammer", Category: CategoryChallenges,
		Metric: EvChallengeUpheld, Tiers: []int{1, 5, 15},
		Describe: func(n int) string { return "Have " + count(n, "challenge", "challenges") + " upheld" },
	},
	{
		Key: "jury_duty", Name: "Jury Duty", Icon: "people", Category: CategoryChallenges,
		Metric: EvChallengeVote, Tiers: []int{10, 40, 120},
		Describe: func(n int) string { return fmt.Sprintf("Cast %d votes on other players' challenges", n) },
	},
	{
		Key: "sore_loser", Name: "Sore Loser", Icon: "emoji-angry", Category: CategoryChallenges, Secret: true,
		Metric: EvChallengeRejected, Tiers: []int{1},
		Describe: func(int) string { return "Have a challenge rejected by the table" },
	},

	// ---- Hosting
	{
		Key: "party_host", Name: "Party Host", Icon: "balloon", Category: CategoryHosting,
		Metric: EvLobbyHosted, Tiers: []int{1, 5, 15, 40},
		Describe: func(n int) string { return "Host " + count(n, "lobby", "lobbies") },
	},
}

// Rules the descriptions above quote, kept as constants so a description and the
// code that enforces it cannot drift apart.
const (
	// StandardCardsToWin is the game length the game-length achievements apply
	// to: the default first-to-ten. A game with any other target is not compared.
	StandardCardsToWin = 10
	// AllInMinStake and RockBottomMinStake are how big a whole-stash wager must
	// be to count, so wagering the single token you had does not.
	AllInMinStake      = 10
	RockBottomMinStake = 8
	// ComebackDeficit is how many cards behind the leader makes a comeback.
	ComebackDeficit = 3
	// FortKnoxDefences is how many steals a player must have fended off.
	FortKnoxDefences = 2
	// FlatBrokePeak is the balance a player must have held before losing it all.
	FlatBrokePeak = 15
	// TimeTravelerPerDecade is how many correct placements count a decade.
	TimeTravelerPerDecade = 15
)

// AchievementByKey returns the catalog entry with key.
func AchievementByKey(key string) (Achievement, bool) {
	for _, a := range Achievements {
		if a.Key == key {
			return a, true
		}
	}
	return Achievement{}, false
}

// TierCount is how many rungs the ladder has.
func (a Achievement) TierCount() int { return len(a.Tiers) }

// Single reports an achievement with one rung: it is earned or it is not.
func (a Achievement) Single() bool { return len(a.Tiers) == 1 }

// TierLabel names a rung (1-based): "Bronze", "Gold". A single achievement has
// none.
func (a Achievement) TierLabel(tier int) string {
	if a.Single() || tier < 1 || tier > len(a.Tiers) {
		return ""
	}
	if tier > len(tierLabels) {
		return tierLabels[len(tierLabels)-1]
	}
	return tierLabels[tier-1]
}

// TierDescription is what a rung (1-based) asks for.
func (a Achievement) TierDescription(tier int) string {
	if tier < 1 || tier > len(a.Tiers) {
		return ""
	}
	return a.Describe(a.Tiers[tier-1])
}

// Reached is how many rungs a metrics set climbs. A Lower achievement needs the
// metric to exist at all (no recorded time is not a time of zero).
func (a Achievement) Reached(m Metrics) int {
	value, ok := m[a.Metric]
	if !ok && a.Lower {
		return 0
	}
	reached := 0
	for _, threshold := range a.Tiers {
		if a.Lower && value > threshold || !a.Lower && value < threshold {
			break
		}
		reached++
	}
	return reached
}

// FormatValue writes a metric value the way the trophy case shows it.
func (a Achievement) FormatValue(value int) string {
	if a.Seconds {
		return formatMinutes(value)
	}
	return fmt.Sprintf("%d", value)
}

// formatMinutes is seconds as "27:05".
func formatMinutes(seconds int) string {
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

// Metrics is one player's numbers, keyed by the Metric* constants and the
// event-derived keys (an event type, or an event type with _max / _min).
type Metrics map[string]int

// AchievementUnlock is a tier a player has just earned.
type AchievementUnlock struct {
	UserId      uuid.UUID
	UserName    string
	Key         string
	Name        string
	Icon        string
	Description string
	Tier        int
	TierCount   int
	TierLabel   string // empty for a single achievement
	Secret      bool
}

// Title is the unlock as one line: "Highway Robber (Gold)".
func (u AchievementUnlock) Title() string {
	if u.TierLabel == "" {
		return u.Name
	}
	return fmt.Sprintf("%s (%s)", u.Name, u.TierLabel)
}
