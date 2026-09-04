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

	hasPlaced, err := database.HasPlaced(ctx.Game.Id, ctx.Player.Id)
	if err != nil {
		hasPlaced = false
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
	// says so. A finished game keeps the vinyl but swaps the answer for a
	// YOU WON/LOST banner, so the answer must not ride along in that HTML.
	var answer database.CurrentCardAnswer
	revealed := ctx.Game.RoundPhase == database.PhaseReveal &&
		ctx.Game.GameStatus != database.StatusFinished
	if revealed {
		if fetched, err := database.GetCurrentCardAnswer(ctx.Game.Id); err == nil {
			answer = fetched
		}
	}

	isWinner := ctx.Game.GameStatus == database.StatusFinished &&
		ctx.Game.WinnerId.Valid &&
		ctx.Game.WinnerId.UUID == ctx.UserId

	type data struct {
		database.CurrentCard
		Answer          database.CurrentCardAnswer
		Revealed        bool
		LobbyId         uuid.UUID
		GameStatus      string
		RoundPhase      string
		IsCurrentPlayer bool
		IsWinner        bool
		HasPlaced       bool
		HasGuessed      bool
		ReplayUsed      bool
		TokenCount      int
		GuessMode       string
	}

	_ = tmpl.Execute(w, data{
		CurrentCard:     card,
		Answer:          answer,
		Revealed:        revealed,
		LobbyId:         ctx.LobbyId,
		GameStatus:      ctx.Game.GameStatus,
		RoundPhase:      ctx.Game.RoundPhase,
		IsCurrentPlayer: isCurrentPlayer,
		IsWinner:        isWinner,
		HasPlaced:       hasPlaced,
		HasGuessed:      hasGuessed,
		ReplayUsed:      ctx.Game.ReplayUsed,
		TokenCount:      tokens,
		GuessMode:       ctx.Game.GuessMode,
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
	// someone else guessed before choosing your own (or before your own
	// steal turn) would make it a copy rather than a judgement.
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

	hasPlaced, err := database.HasPlaced(ctx.Game.Id, ctx.Player.Id)
	if err != nil {
		hasPlaced = false
	}

	isCurrentPlayer := ctx.Game.CurrentPlayerId.Valid && ctx.Game.CurrentPlayerId.UUID == ctx.Player.Id

	// A player may place when it is their turn during listening, or attempt a
	// steal when they are specifically the one who claimed the sole steal
	// attempt this round.
	canPlace := isCurrentPlayer && ctx.Game.RoundPhase == database.PhaseListening && !hasPlaced
	canSteal := ctx.Game.RoundPhase == database.PhaseStealTurn &&
		ctx.Game.StealerPlayerId.Valid &&
		ctx.Game.StealerPlayerId.UUID == ctx.Player.Id

	tmpl, err := template.New("timeline.html").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
	}).ParseFS(static.StaticFiles, "html/components/tracktimeline/timeline.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse template."))
		return
	}

	type data struct {
		Timelines   []database.PlayerTimeline
		LobbyId     uuid.UUID
		GameStatus  string
		RoundPhase  string
		CanPlace    bool
		CanSteal    bool
		TokenCount  int
		CardsToWin  int
	}

	_ = tmpl.Execute(w, data{
		Timelines:  timelines,
		LobbyId:    ctx.LobbyId,
		GameStatus: ctx.Game.GameStatus,
		RoundPhase: ctx.Game.RoundPhase,
		CanPlace:   canPlace,
		CanSteal:   canSteal,
		TokenCount: tokens,
		CardsToWin: ctx.Game.CardsToWin,
	})
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
