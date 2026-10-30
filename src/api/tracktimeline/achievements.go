package apiTrackTimeline

import (
	"encoding/json"
	"fmt"
	"log"

	gsWebsocket "github.com/gerp93/gameshell-framework/websocket"
	"github.com/google/uuid"

	apiRoom "github.com/gerp93/track-timeline/api/room"
	"github.com/gerp93/track-timeline/database"
)

// Achievements are earned by what happens in a round, but never shown while the
// round is still being decided: an unlock that popped up at lock-in would say a
// placement was right before the steal window had closed. So the moments are
// recorded as they happen (see database.LogAchievementEvent), and the unlocks
// they lead to are worked out once, as the round ends, and sent with the reveal.
//
// Nothing here may stop the game. Every failure is logged and the round carries
// on without the unlock.

// roundFacts is what finishRound needs to know about the round that just ended
// beyond its outcome: who, if anyone, had claimed the steal. The request that
// ended the round read the game before resolving it, and resolving clears the
// claim, so it is carried here rather than read back.
type roundFacts struct {
	outcome         database.RoundOutcome
	stealerPlayerId uuid.NullUUID
}

// logEvent records a moment for a player, logging rather than returning a failure.
func logEvent(userId uuid.UUID, gameId uuid.UUID, eventType string, value int) {
	if userId == uuid.Nil {
		return
	}
	if err := database.LogAchievementEvent(userId, gameId, eventType, value); err != nil {
		log.Println(err)
	}
}

// logPlacementEvents records what a judged placement says about its player: a
// correct one squeezed between two neighbours a year apart, a miss by a single
// year. Called at lock-in, silently; the unlock waits for the reveal.
func logPlacementEvents(userId uuid.UUID, gameId uuid.UUID, yearRange database.PlacementYearRange, releaseYear int, correct bool) {
	switch {
	case correct && yearRange.Narrow():
		logEvent(userId, gameId, database.EvNeedle, 1)
	case !correct && yearRange.MissBy(releaseYear) == 1:
		logEvent(userId, gameId, database.EvSoClose, 1)
	}
}

// awardRound records everything the round that just ended adds to the
// achievements, then works out who has unlocked what. winnerUserId is the game's
// winner when this round ended it, or uuid.Nil.
func awardRound(ctx gameContext, facts roundFacts, winnerUserId uuid.UUID) []database.AchievementUnlock {
	gameId := ctx.Game.Id
	players, err := database.GetPlayers(gameId)
	if err != nil {
		log.Println(err)
		return nil
	}
	userOf := make(map[uuid.UUID]uuid.UUID, len(players))
	userIds := make([]uuid.UUID, 0, len(players))
	for _, p := range players {
		userOf[p.PlayerId] = p.UserId
		userIds = append(userIds, p.UserId)
	}
	outcome := facts.outcome

	turnUser := uuid.Nil
	if outcome.CurrentPlayerId.Valid {
		turnUser = userOf[outcome.CurrentPlayerId.UUID]
	}

	// A steal that took the card took it from the player on turn; one that failed
	// against a placement that was right is a steal defended.
	if facts.stealerPlayerId.Valid {
		switch {
		case outcome.WonByChallenge:
			logEvent(turnUser, gameId, database.EvStolenFrom, 1)
		case outcome.WinnerPlayerId.Valid:
			logEvent(turnUser, gameId, database.EvStealDefended, 1)
		}
	}

	// The player on turn placed it and named it: a double dip, and with the exact
	// year on top, a triple crown.
	if turnUser != uuid.Nil && outcome.WinnerPlayerId.Valid && !outcome.WonByChallenge {
		for _, g := range outcome.Guesses {
			if g.UserId != turnUser || g.CardId != outcome.CardId || !g.Judged || !g.TitleCorrect || !g.ArtistCorrect {
				continue
			}
			logEvent(turnUser, gameId, database.EvDoubleDip, 1)
			if outcome.HasExactYearGuess && outcome.ExactYearCorrect {
				logEvent(turnUser, gameId, database.EvTripleCrown, 1)
			}
			break
		}
	}

	recordBalances(gameId, players)
	if winnerUserId != uuid.Nil {
		recordWin(ctx, players, userOf, winnerUserId, outcome)
	}

	unlocks, err := database.EvaluateAchievements(userIds)
	if err != nil {
		log.Println(err)
		return nil
	}
	return unlocks
}

// recordBalances notes, at the end of a round, how well off each player is and how
// far behind: the peak of their token balance this game (and a stash lost
// entirely), and the first time they trailed the leader by enough to call coming
// back from it a comeback.
func recordBalances(gameId uuid.UUID, players []database.Player) {
	for _, p := range players {
		if !p.IsActive {
			continue
		}

		leader := 0
		for _, other := range players {
			if other.IsActive && other.PlayerId != p.PlayerId {
				leader = max(leader, other.TimelineSize)
			}
		}
		if deficit := leader - p.TimelineSize; deficit >= database.ComebackDeficit {
			if n, err := database.CountGameEvents(p.UserId, gameId, database.EvDeficit); err == nil && n == 0 {
				logEvent(p.UserId, gameId, database.EvDeficit, deficit)
			}
		}

		peak, err := database.MaxGameEventValue(p.UserId, gameId, database.EvPeakTokens)
		if err != nil {
			log.Println(err)
			continue
		}
		if p.TokenCount >= database.FlatBrokePeak && p.TokenCount > peak {
			logEvent(p.UserId, gameId, database.EvPeakTokens, p.TokenCount)
			peak = p.TokenCount
		}
		if p.TokenCount == 0 && peak >= database.FlatBrokePeak {
			if n, err := database.CountGameEvents(p.UserId, gameId, database.EvFlatBroke); err == nil && n == 0 {
				logEvent(p.UserId, gameId, database.EvFlatBroke, 1)
			}
		}
	}
}

// recordWin notes what the winning of a game says about the winner: how it was
// won, how long it took, what the timeline looked like and what the rest of the
// table was doing. A game played alone proves nothing about any of it.
func recordWin(ctx gameContext, players []database.Player, userOf map[uuid.UUID]uuid.UUID, winnerUserId uuid.UUID, outcome database.RoundOutcome) {
	gameId := ctx.Game.Id

	var winner database.Player
	active := 0
	for _, p := range players {
		if p.IsActive {
			active++
		}
		if p.UserId == winnerUserId {
			winner = p
		}
	}
	if active < 2 || winner.PlayerId == uuid.Nil {
		return
	}

	if timeline, err := database.GetPlayerTimeline(gameId, winner.PlayerId); err != nil {
		log.Println(err)
	} else if len(timeline) > 0 {
		oldest, newest := timeline[0].ReleaseYear, timeline[0].ReleaseYear
		for _, card := range timeline {
			oldest = min(oldest, card.ReleaseYear)
			newest = max(newest, card.ReleaseYear)
		}
		logEvent(winnerUserId, gameId, database.EvWinSpan, newest-oldest)
	}

	// Only the standard first-to-ten is timed: a first-to-five is not a quick
	// first-to-ten, and a first-to-twenty is not a slow one.
	if ctx.Game.CardsToWin == database.StandardCardsToWin {
		if seconds, ok, err := database.GameSeconds(gameId); err != nil {
			log.Println(err)
		} else if ok {
			logEvent(winnerUserId, gameId, database.EvWinSeconds, seconds)
		}
	}

	if misses, ok, err := database.GameOwnTurnMisses(winnerUserId, gameId); err != nil {
		log.Println(err)
	} else if ok && misses == 0 {
		logEvent(winnerUserId, gameId, database.EvFlawless, 1)
	}

	if n, err := database.CountGameEvents(winnerUserId, gameId, database.EvDeficit); err == nil && n > 0 {
		logEvent(winnerUserId, gameId, database.EvComeback, 1)
	}

	for _, p := range players {
		if p.IsActive && p.UserId != winnerUserId && p.TimelineSize == ctx.Game.CardsToWin-1 {
			logEvent(winnerUserId, gameId, database.EvPhotoFinish, 1)
			break
		}
	}

	if outcome.WonByChallenge && outcome.WinnerPlayerId.Valid && userOf[outcome.WinnerPlayerId.UUID] == winnerUserId {
		logEvent(winnerUserId, gameId, database.EvHeist, 1)
	}

	defended, defErr := database.CountGameEvents(winnerUserId, gameId, database.EvStealDefended)
	stolen, stolenErr := database.CountGameEvents(winnerUserId, gameId, database.EvStolenFrom)
	if defErr == nil && stolenErr == nil && defended >= database.FortKnoxDefences && stolen == 0 {
		logEvent(winnerUserId, gameId, database.EvFortKnox, 1)
	}
}

// unlockPayload is one entry of the achievements: websocket message.
type unlockPayload struct {
	Player      string `json:"player"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
	Tier        int    `json:"tier"`
	TierCount   int    `json:"tierCount"`
	TierLabel   string `json:"tierLabel,omitempty"`
	Secret      bool   `json:"secret,omitempty"`
}

// sendUnlocks tells the table about what was just unlocked: one line each in chat,
// and a single achievements: message the page turns into a pop-up once whatever is
// already on screen (the reveal, a challenge) has gone.
func sendUnlocks(lobbyId uuid.UUID, unlocks []database.AchievementUnlock) {
	if len(unlocks) == 0 {
		return
	}

	payloads := make([]unlockPayload, 0, len(unlocks))
	for _, u := range unlocks {
		announce(lobbyId, fmt.Sprintf("<green>%s</> unlocked <blue>%s</> — %s",
			esc(u.UserName), esc(u.Title()), esc(u.Description)))
		payloads = append(payloads, unlockPayload{
			Player:      u.UserName,
			Name:        u.Name,
			Icon:        u.Icon,
			Description: u.Description,
			Tier:        u.Tier,
			TierCount:   u.TierCount,
			TierLabel:   u.TierLabel,
			Secret:      u.Secret,
		})
	}

	encoded, err := json.Marshal(payloads)
	if err != nil {
		log.Println(err)
		return
	}
	gsWebsocket.LobbyBroadcast(lobbyId, "achievements:"+string(encoded))
	apiRoom.MirrorBroadcast(lobbyId, "achievements:"+string(encoded))
}

// awardUsers works out and announces the unlocks of specific players, for the
// moments that happen outside a round (a challenge being settled).
func awardUsers(lobbyId uuid.UUID, userIds []uuid.UUID) {
	unlocks, err := database.EvaluateAchievements(userIds)
	if err != nil {
		log.Println(err)
		return
	}
	sendUnlocks(lobbyId, unlocks)
}
