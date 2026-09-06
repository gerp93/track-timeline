package apiRoom

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/static"
)

func loadHostRoom(w http.ResponseWriter, r *http.Request) (database.Room, database.Game, bool) {
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	room, err := database.GetRoomByCode(code)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Room not found."))
		return database.Room{}, database.Game{}, false
	}
	token, ok := readHostToken(r, room.Code)
	if !ok || token != room.HostToken {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Host token missing or invalid."))
		return database.Room{}, database.Game{}, false
	}
	game, err := database.GetGame(room.LobbyId)
	if err != nil || game.Id == uuid.Nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to load game."))
		return database.Room{}, database.Game{}, false
	}
	return room, game, true
}

// HostCurrentCard renders the song-in-play chrome for the TV (no turn controls).
func HostCurrentCard(w http.ResponseWriter, r *http.Request) {
	room, game, ok := loadHostRoom(w, r)
	if !ok {
		return
	}

	card, err := database.GetCurrentCard(game.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get the current song."))
		return
	}

	var answer database.CurrentCardAnswer
	revealed := game.RoundPhase == database.PhaseReveal && game.GameStatus != database.StatusFinished
	if revealed {
		if fetched, err := database.GetCurrentCardAnswer(game.Id); err == nil {
			answer = fetched
		}
	}

	tmpl, err := template.ParseFS(static.StaticFiles, "html/components/tracktimeline/current-card.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse template."))
		return
	}

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
		IsRoom          bool
		IsHostDisplay   bool
	}
	_ = tmpl.Execute(w, data{
		CurrentCard:   card,
		Answer:        answer,
		Revealed:      revealed,
		LobbyId:       room.LobbyId,
		GameStatus:    game.GameStatus,
		RoundPhase:    game.RoundPhase,
		ReplayUsed:    game.ReplayUsed,
		GuessMode:     game.GuessMode,
		IsRoom:        true,
		IsHostDisplay: true,
	})
}

// HostTimeline renders every seat's timeline for the TV (no placement UI).
func HostTimeline(w http.ResponseWriter, r *http.Request) {
	room, game, ok := loadHostRoom(w, r)
	if !ok {
		return
	}

	currentPlayerId := uuid.Nil
	if game.CurrentPlayerId.Valid {
		currentPlayerId = game.CurrentPlayerId.UUID
	}
	revealPositions := game.RoundPhase == database.PhaseReveal

	timelines, err := database.GetAllPlayerTimelines(game.Id, currentPlayerId, uuid.Nil, revealPositions)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to load timelines."))
		return
	}

	currentPlayerName := ""
	for _, t := range timelines {
		if t.IsCurrent {
			currentPlayerName = t.PlayerName
		}
	}

	guessedCount := 0
	if game.GuessMode != database.GuessModeOff {
		if guesses, guessErr := database.GetGuesses(game.Id); guessErr == nil {
			guessedCount = len(guesses)
		}
	}

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
		Timelines         []database.PlayerTimeline
		LobbyId           uuid.UUID
		GameStatus        string
		RoundPhase        string
		CanPlace          bool
		CanSteal          bool
		TokenCount        int
		CardsToWin        int
		InLead            bool
		BuyCardCost       int
		CurrentPlayerName string
		GuessMode         string
		GuessedCount      int
		ActivePlayerCount int
	}
	_ = tmpl.Execute(w, data{
		Timelines:         timelines,
		LobbyId:           room.LobbyId,
		GameStatus:        game.GameStatus,
		RoundPhase:        game.RoundPhase,
		CardsToWin:        game.CardsToWin,
		BuyCardCost:       database.BuyCardCost,
		CurrentPlayerName: currentPlayerName,
		GuessMode:         game.GuessMode,
		GuessedCount:      guessedCount,
		ActivePlayerCount: len(timelines),
	})
}
