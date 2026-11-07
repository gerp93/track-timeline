package database

import (
	"errors"
	"log"

	"github.com/google/uuid"
)

// GameTrophy is one achievement tier somebody unlocked during a game, as the
// lobby's trophy drawer lists it.
type GameTrophy struct {
	UserName    string
	Name        string
	Icon        string
	Description string
	TierLabel   string // empty for a single achievement
	Secret      bool
}

// Title is the trophy as one line: "Highway Robber (Gold)".
func (t GameTrophy) Title() string {
	if t.TierLabel == "" {
		return t.Name
	}
	return t.Name + " (" + t.TierLabel + ")"
}

// TierClass is the tier as a CSS class suffix: "gold", or "" for a single.
func (t GameTrophy) TierClass() string {
	return TrophyRung{Label: t.TierLabel}.LabelClass()
}

// GetGameTrophies lists every achievement tier unlocked by any player of the game
// since it started, newest first. A lobby is reused across rematches, so "this
// game" means since STARTED_ON_DATE, not merely by someone in the lobby; a game
// that has not started has won nothing. Tiers granted silently from earlier play
// (BACKFILLED) were not won in front of the table and are left out.
//
// Tiers the catalog no longer knows are skipped rather than failing the list.
func GetGameTrophies(gameId uuid.UUID) ([]GameTrophy, error) {
	rows, err := query(`
		SELECT U.NAME, A.ACHIEVEMENT_KEY, A.TIER
		FROM TRACK_TIMELINE_GAME G
			INNER JOIN PLAYER P ON P.LOBBY_ID = G.LOBBY_ID
			INNER JOIN USER U ON U.ID = P.USER_ID
			INNER JOIN TRACK_TIMELINE_ACHIEVEMENT_UNLOCK A ON A.USER_ID = P.USER_ID
		WHERE G.ID = ?
			AND G.STARTED_ON_DATE IS NOT NULL
			AND A.CREATED_ON_DATE >= G.STARTED_ON_DATE
			AND A.BACKFILLED = 0
		ORDER BY A.CREATED_ON_DATE DESC, A.TIER DESC, U.NAME ASC
	`, gameId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	trophies := make([]GameTrophy, 0)
	for rows.Next() {
		var userName, key string
		var tier int
		if err := rows.Scan(&userName, &key, &tier); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		a, ok := AchievementByKey(key)
		if !ok || tier < 1 || tier > a.TierCount() {
			continue
		}
		trophies = append(trophies, GameTrophy{
			UserName:    userName,
			Name:        a.Name,
			Icon:        a.Icon,
			Description: a.TierDescription(tier),
			TierLabel:   a.TierLabel(tier),
			Secret:      a.Secret,
		})
	}
	return trophies, nil
}
