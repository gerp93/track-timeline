package database

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// achievementMu serializes evaluation, so two rounds ending at once (two lobbies,
// or a steal and its timeout) cannot both find the same tier newly reached and
// announce it twice. Evaluation is a handful of reads and a few inserts, and a
// round ends rarely, so one process-wide lock costs nothing.
var achievementMu sync.Mutex

// heldTiers maps user -> achievement key -> the highest tier unlocked.
type heldTiers map[uuid.UUID]map[string]int

func loadHeldTiers() (heldTiers, error) {
	rows, err := query(`
		SELECT USER_ID, ACHIEVEMENT_KEY, MAX(TIER)
		FROM TRACK_TIMELINE_ACHIEVEMENT_UNLOCK
		GROUP BY USER_ID, ACHIEVEMENT_KEY
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	held := make(heldTiers)
	for rows.Next() {
		var userId uuid.UUID
		var key string
		var tier int
		if err := rows.Scan(&userId, &key, &tier); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		if held[userId] == nil {
			held[userId] = make(map[string]int)
		}
		held[userId][key] = tier
	}
	return held, nil
}

// loadAchievementMetrics computes every approved player's numbers from the logs.
func loadAchievementMetrics() (map[uuid.UUID]Metrics, statsSnapshot, error) {
	snapshot, err := loadStatsSnapshot()
	if err != nil {
		return nil, snapshot, err
	}
	events, err := loadAchievementEvents()
	if err != nil {
		return nil, snapshot, err
	}
	games := reconstructGames(snapshot.wins, snapshot.placements)
	return buildAchievementMetrics(snapshot, games, events), snapshot, nil
}

// EvaluateAchievements unlocks every tier the given players have newly reached
// and returns what was unlocked, one entry per achievement (the highest tier
// climbed, if a player jumped several at once). Only approved accounts earn
// achievements. Called when a round ends, so what was unlocked can be shown to
// the table with the reveal.
func EvaluateAchievements(userIds []uuid.UUID) ([]AchievementUnlock, error) {
	return evaluateAchievements(userIds, false)
}

// BackfillAchievements silently unlocks everything every approved player has
// already earned, without announcing it: the history that was there when
// achievements were switched on, or when a new one was added to the catalog,
// should not arrive as a flood of pop-ups. Safe to run on every start.
func BackfillAchievements() error {
	_, err := evaluateAchievements(nil, true)
	return err
}

func evaluateAchievements(userIds []uuid.UUID, backfill bool) ([]AchievementUnlock, error) {
	achievementMu.Lock()
	defer achievementMu.Unlock()

	metrics, snapshot, err := loadAchievementMetrics()
	if err != nil {
		return nil, err
	}
	held, err := loadHeldTiers()
	if err != nil {
		return nil, err
	}

	targets := userIds
	if backfill {
		targets = targets[:0]
		for id := range metrics {
			targets = append(targets, id)
		}
	}

	var unlocked []AchievementUnlock
	seen := make(map[uuid.UUID]bool)
	for _, userId := range targets {
		if seen[userId] {
			continue
		}
		seen[userId] = true
		m, ok := metrics[userId]
		if !ok {
			continue // a guest, or somebody who is gone
		}

		for _, a := range Achievements {
			reached := a.Reached(m)
			have := held[userId][a.Key]
			if reached <= have {
				continue
			}
			failed := false
			for tier := have + 1; tier <= reached; tier++ {
				if err := insertUnlock(userId, a.Key, tier, backfill); err != nil {
					log.Println(err)
					failed = true
					break
				}
			}
			if failed || backfill {
				continue
			}
			unlocked = append(unlocked, AchievementUnlock{
				UserId:      userId,
				UserName:    snapshot.users[userId].Name,
				Key:         a.Key,
				Name:        a.Name,
				Icon:        a.Icon,
				Description: a.TierDescription(reached),
				Tier:        reached,
				TierCount:   a.TierCount(),
				TierLabel:   a.TierLabel(reached),
				Secret:      a.Secret,
			})
		}
	}
	return unlocked, nil
}

// insertUnlock records one tier. INSERT IGNORE leaves an existing row alone, so
// the unlock date is always the first one.
func insertUnlock(userId uuid.UUID, key string, tier int, backfilled bool) error {
	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return errors.New("failed to generate new id")
	}
	return execute(`
		INSERT IGNORE INTO TRACK_TIMELINE_ACHIEVEMENT_UNLOCK (ID, USER_ID, ACHIEVEMENT_KEY, TIER, BACKFILLED)
		VALUES (?, ?, ?, ?, ?)
	`, id, userId, key, tier, backfilled)
}

// ---- trophy case

// TrophyRung is one tier of an achievement as the trophy case shows it.
type TrophyRung struct {
	Number      int
	Label       string
	Description string
	Unlocked    bool
	UnlockedOn  time.Time
	Backfilled  bool // unlocked from earlier play, so the date is not known
}

// Trophy is one achievement as the trophy case shows it for a player.
type Trophy struct {
	Key      string
	Name     string
	Icon     string
	Category string
	Secret   bool
	Single   bool

	Rungs    []TrophyRung
	Reached  int // tiers unlocked
	Complete bool

	// Progress toward the next rung, empty when the ladder is complete or there
	// is nothing to measure (a single rung of 1 is earned or it is not).
	// Progress is words ("7 / 10", "best 27:05 · aim for 25:00"); Percent is
	// 0-100 and only means anything when HasBar.
	Progress string
	Percent  int
	HasBar   bool

	// Next is what the next rung asks for.
	Next string
}

// Unlocked reports at least one tier earned.
func (t Trophy) Unlocked() bool { return t.Reached > 0 }

// Medal is the label of the highest tier earned, or "" for a single achievement
// or one not yet started.
func (t Trophy) Medal() string {
	if t.Reached == 0 || t.Reached > len(t.Rungs) {
		return ""
	}
	return t.Rungs[t.Reached-1].Label
}

// MedalClass is the medal as a CSS class suffix: "gold".
func (t Trophy) MedalClass() string { return strings.ToLower(t.Medal()) }

// LabelClass is the rung's tier as a CSS class suffix: "gold".
func (r TrophyRung) LabelClass() string { return strings.ToLower(r.Label) }

// TrophyCategory groups a player's trophies.
type TrophyCategory struct {
	Name     string
	Trophies []Trophy
	Earned   int
}

// TrophyCase is everything the trophy page shows for one player.
type TrophyCase struct {
	UserId   uuid.UUID
	UserName string

	Categories []TrophyCategory
	// Earned counts achievements with at least one tier; Total is the achievements
	// that can be seen (a secret one is not counted until it is unlocked).
	Earned int
	Total  int
	// Tiers counts every tier earned, of every achievement.
	Tiers int
	// HiddenSecrets is how many secret achievements are still locked.
	HiddenSecrets int
}

// GetTrophyCase builds a player's trophy case. found is false for an account
// that cannot earn achievements (a guest, or nobody at all). Secret achievements
// that are still locked are counted but never described.
func GetTrophyCase(userId uuid.UUID) (TrophyCase, bool, error) {
	var trophyCase TrophyCase

	metrics, snapshot, err := loadAchievementMetrics()
	if err != nil {
		return trophyCase, false, err
	}
	user, ok := snapshot.users[userId]
	if !ok || !user.Approved {
		return trophyCase, false, nil
	}
	m := metrics[userId]

	rows, err := query(`
		SELECT ACHIEVEMENT_KEY, TIER, CREATED_ON_DATE, BACKFILLED
		FROM TRACK_TIMELINE_ACHIEVEMENT_UNLOCK
		WHERE USER_ID = ?
	`, userId)
	if err != nil {
		return trophyCase, false, err
	}
	type unlockRow struct {
		on         time.Time
		backfilled bool
	}
	unlocks := make(map[string]map[int]unlockRow)
	for rows.Next() {
		var key string
		var tier int
		var row unlockRow
		if err := rows.Scan(&key, &tier, &row.on, &row.backfilled); err != nil {
			rows.Close()
			log.Println(err)
			return trophyCase, false, errors.New("failed to scan row in query results")
		}
		if unlocks[key] == nil {
			unlocks[key] = make(map[int]unlockRow)
		}
		unlocks[key][tier] = row
	}
	rows.Close()

	trophyCase.UserId = userId
	trophyCase.UserName = user.Name

	byCategory := make(map[string]*TrophyCategory)
	for _, name := range AchievementCategories {
		byCategory[name] = &TrophyCategory{Name: name}
	}

	for _, a := range Achievements {
		trophy := buildTrophy(a, m, func(tier int) (time.Time, bool, bool) {
			row, ok := unlocks[a.Key][tier]
			return row.on, row.backfilled, ok
		})
		if trophy.Secret && !trophy.Unlocked() {
			trophyCase.HiddenSecrets++
			continue
		}
		category := byCategory[a.Category]
		category.Trophies = append(category.Trophies, trophy)
		trophyCase.Total++
		trophyCase.Tiers += trophy.Reached
		if trophy.Unlocked() {
			category.Earned++
			trophyCase.Earned++
		}
	}

	for _, name := range AchievementCategories {
		if category := byCategory[name]; len(category.Trophies) > 0 {
			trophyCase.Categories = append(trophyCase.Categories, *category)
		}
	}
	return trophyCase, true, nil
}

// buildTrophy turns a catalog entry, a player's metrics and what they have
// unlocked into the trophy case's view of it. unlocked reports a tier's unlock
// date, whether it was a backfill, and whether it is unlocked at all.
func buildTrophy(a Achievement, m Metrics, unlocked func(tier int) (time.Time, bool, bool)) Trophy {
	trophy := Trophy{
		Key:      a.Key,
		Name:     a.Name,
		Icon:     a.Icon,
		Category: a.Category,
		Secret:   a.Secret,
		Single:   a.Single(),
	}

	// What they have unlocked is what counts: the stored rows, not a fresh read of
	// the metrics, so a number that later dips (a song's year corrected) does not
	// take a trophy back. The metric only drives the progress toward the next rung.
	for i := range a.Tiers {
		tier := i + 1
		rung := TrophyRung{Number: tier, Label: a.TierLabel(tier), Description: a.TierDescription(tier)}
		if on, backfilled, ok := unlocked(tier); ok {
			rung.Unlocked, rung.UnlockedOn, rung.Backfilled = true, on, backfilled
			trophy.Reached = tier
		}
		trophy.Rungs = append(trophy.Rungs, rung)
	}
	trophy.Complete = trophy.Reached == len(a.Tiers)
	if trophy.Complete {
		return trophy
	}

	threshold := a.Tiers[trophy.Reached]
	trophy.Next = a.TierDescription(trophy.Reached + 1)
	value, has := m[a.Metric]
	switch {
	case a.Seconds && !has:
		// No game of the standard length has been won yet, so there is no time to
		// measure (a time of 0:00 would read as one).
		trophy.Progress = fmt.Sprintf("no time yet · aim for %s", a.FormatValue(threshold))
	case a.Lower:
		trophy.Progress = fmt.Sprintf("best %s · aim for %s", a.FormatValue(value), a.FormatValue(threshold))
		trophy.HasBar = true
		trophy.Percent = clampPercent(100 * threshold / max(value, 1))
	case threshold > 1:
		trophy.Progress = fmt.Sprintf("%s / %s", a.FormatValue(value), a.FormatValue(threshold))
		trophy.HasBar = true
		trophy.Percent = clampPercent(100 * value / threshold)
	}
	return trophy
}

func clampPercent(p int) int {
	return min(max(p, 0), 100)
}
