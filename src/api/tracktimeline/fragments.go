package apiTrackTimeline

import (
	"html/template"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/static"
)

// The fragment endpoints below are the boundary the answer must not cross
// before the reveal. The rule is enforced by which type gets fetched, not by
// which fields a template happens to reference: database.CurrentCard has no
// title, artist or year field to leak, and database.CurrentCardAnswer is only
// ever fetched once ROUND_PHASE has reached 'reveal'.

// GetCurrentCard renders the song in play.
func GetCurrentCard(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}

	card, err := database.GetCurrentCard(ctx.Game.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get the current song."))
		return
	}

	tokens, err := database.GetPlayerTokens(ctx.Game.Id, ctx.Player.Id)
	if err != nil {
		tokens = 0
	}

	hasPlaced := false
	if placements, err := database.GetPlacements(ctx.Game.Id); err == nil {
		for _, placement := range placements {
			if placement.PlayerId == ctx.Player.Id {
				hasPlaced = true
				break
			}
		}
	}

	isCurrentPlayer := ctx.Game.CurrentPlayerId.Valid && ctx.Game.CurrentPlayerId.UUID == ctx.Player.Id

	hasGuessed, err := database.HasGuessed(ctx.Game.Id, ctx.Player.Id)
	if err != nil {
		hasGuessed = true
	}

	tmpl, err := template.ParseFS(static.StaticFiles, "html/components/tracktimeline/current-card.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse template."))
		return
	}

	// Only the reveal fragment is given the answer, and only once the phase
	// says so.
	var answer database.CurrentCardAnswer
	revealed := ctx.Game.RoundPhase == database.PhaseReveal
	if revealed {
		if fetched, err := database.GetCurrentCardAnswer(ctx.Game.Id); err == nil {
			answer = fetched
		}
	}

	type data struct {
		database.CurrentCard
		Answer          database.CurrentCardAnswer
		Revealed        bool
		LobbyId         uuid.UUID
		GameStatus      string
		RoundPhase      string
		IsCurrentPlayer bool
		HasPlaced       bool
		HasGuessed      bool
		TokenCount      int
	}

	_ = tmpl.Execute(w, data{
		CurrentCard:     card,
		Answer:          answer,
		Revealed:        revealed,
		LobbyId:         ctx.LobbyId,
		GameStatus:      ctx.Game.GameStatus,
		RoundPhase:      ctx.Game.RoundPhase,
		IsCurrentPlayer: isCurrentPlayer,
		HasPlaced:       hasPlaced,
		HasGuessed:      hasGuessed,
		TokenCount:      tokens,
	})
}

// GetTimeline renders the whole board from this viewer's point of view.
func GetTimeline(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}

	currentPlayerId := uuid.Nil
	if ctx.Game.CurrentPlayerId.Valid {
		currentPlayerId = ctx.Game.CurrentPlayerId.UUID
	}

	// Rival placement positions stay hidden until the reveal: seeing where
	// someone else guessed before choosing your own would make a challenge a
	// copy rather than a judgement.
	revealPositions := ctx.Game.RoundPhase == database.PhaseReveal

	timelines, err := database.GetAllPlayerTimelines(ctx.Game.Id, currentPlayerId, ctx.Player.Id, revealPositions)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get the board."))
		return
	}

	tokens, err := database.GetPlayerTokens(ctx.Game.Id, ctx.Player.Id)
	if err != nil {
		tokens = 0
	}

	hasPlaced := false
	if placements, err := database.GetPlacements(ctx.Game.Id); err == nil {
		for _, placement := range placements {
			if placement.PlayerId == ctx.Player.Id {
				hasPlaced = true
				break
			}
		}
	}

	isCurrentPlayer := ctx.Game.CurrentPlayerId.Valid && ctx.Game.CurrentPlayerId.UUID == ctx.Player.Id

	// A player may place when it is their turn during listening, or challenge
	// during the window if they hold a token and have not already acted.
	canPlace := isCurrentPlayer && ctx.Game.RoundPhase == database.PhaseListening && !hasPlaced
	canChallenge := !isCurrentPlayer && ctx.Game.RoundPhase == database.PhaseChallenge && !hasPlaced && tokens > 0

	tmpl, err := template.New("timeline.html").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
	}).ParseFS(static.StaticFiles, "html/components/tracktimeline/timeline.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse template."))
		return
	}

	type data struct {
		Timelines    []database.PlayerTimeline
		LobbyId      uuid.UUID
		GameStatus   string
		RoundPhase   string
		CanPlace     bool
		CanChallenge bool
		TokenCount   int
	}

	_ = tmpl.Execute(w, data{
		Timelines:    timelines,
		LobbyId:      ctx.LobbyId,
		GameStatus:   ctx.Game.GameStatus,
		RoundPhase:   ctx.Game.RoundPhase,
		CanPlace:     canPlace,
		CanChallenge: canChallenge,
		TokenCount:   tokens,
	})
}

// GetPlayers renders the player list with timeline sizes and token counts.
func GetPlayers(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}

	players, err := database.GetPlayers(ctx.Game.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get players."))
		return
	}

	tmpl, err := template.ParseFS(static.StaticFiles, "html/components/tracktimeline/players.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse template."))
		return
	}

	type data struct {
		Players    []database.Player
		CardsToWin int
	}

	_ = tmpl.Execute(w, data{Players: players, CardsToWin: ctx.Game.CardsToWin})
}

// GetDrawPileCount returns the bare number of songs left.
func GetDrawPileCount(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}

	count, err := database.GetDrawPileCount(ctx.Game.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("0"))
		return
	}

	_, _ = w.Write([]byte(strconv.Itoa(count)))
}

// GetDecks renders which decks fed this game's pile.
func GetDecks(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}

	decks, err := database.GetGameDecks(ctx.Game.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get decks."))
		return
	}

	tmpl, err := template.ParseFS(static.StaticFiles, "html/components/tracktimeline/deck-info.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse template."))
		return
	}

	_ = tmpl.Execute(w, struct{ Decks []database.DeckInfo }{Decks: decks})
}
