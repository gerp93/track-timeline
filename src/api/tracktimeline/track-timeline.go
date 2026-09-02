package apiTrackTimeline

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"

	gsApi "github.com/gerp93/gameshell-framework/api"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	gsWebsocket "github.com/gerp93/gameshell-framework/websocket"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/static"
)

// resultPayload is the one structured message on the socket. Everything else is
// a short control string; this carries the reveal, which has too many moving
// parts to encode in one.
type resultPayload struct {
	Type           string `json:"type"` // won | discarded
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	ReleaseYear    int    `json:"releaseYear"`
	WinnerName     string `json:"winnerName,omitempty"`
	WonByChallenge bool   `json:"wonByChallenge,omitempty"`
	BottomMessage  string `json:"bottomMessage"`
	UserId         string `json:"userId,omitempty"`
	Celebration    string `json:"celebration,omitempty"`
	HasGif         bool   `json:"hasGif,omitempty"`
	NextPlayerName string `json:"nextPlayerName,omitempty"`
	GameOver       bool   `json:"gameOver,omitempty"`
}

// songPayload tells every client which song to cue and where to start it.
type songPayload struct {
	VideoId      string `json:"videoId"`
	StartSeconds int    `json:"startSeconds"`
}

// esc escapes text bound for a chat line. The framework only escapes messages
// arriving from players over the socket, and the shared chat renderer writes
// with innerHTML, so anything the server interpolates has to be escaped here.
func esc(s string) string {
	return html.EscapeString(s)
}

// announce posts a chat line to the lobby. A bare string with no prefix is
// rendered as chat.
func announce(lobbyId uuid.UUID, message string) {
	gsWebsocket.LobbyBroadcast(lobbyId, message)
}

// sendStatus updates the bottom status line for everyone, with no popup.
func sendStatus(lobbyId uuid.UUID, message string) {
	gsWebsocket.LobbyBroadcast(lobbyId, "status:"+message)
}

// sendResult drives the reveal popup and the status line together.
func sendResult(lobbyId uuid.UUID, payload resultPayload) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		log.Println(err)
		return
	}
	gsWebsocket.LobbyBroadcast(lobbyId, "result:"+string(encoded))
}

// sendSong cues the same song on every client.
func sendSong(lobbyId uuid.UUID, card database.CurrentCard) {
	encoded, err := json.Marshal(songPayload{
		VideoId:      card.YouTubeVideoId,
		StartSeconds: card.StartOffsetSeconds,
	})
	if err != nil {
		log.Println(err)
		return
	}
	gsWebsocket.LobbyBroadcast(lobbyId, "song:"+string(encoded))
}

func refresh(lobbyId uuid.UUID) {
	gsWebsocket.LobbyBroadcast(lobbyId, "refresh")
}

// gameContext is everything a gameplay handler needs about who is acting.
type gameContext struct {
	LobbyId uuid.UUID
	Game    database.Game
	Player  gsDatabase.Player
	UserId  uuid.UUID
}

// loadContext resolves the lobby, its game and the acting player, writing its
// own response and returning false on any problem.
func loadContext(w http.ResponseWriter, r *http.Request) (gameContext, bool) {
	var ctx gameContext

	lobbyId, err := uuid.Parse(r.PathValue("lobbyId"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid lobby."))
		return ctx, false
	}
	ctx.LobbyId = lobbyId

	game, err := database.GetGame(lobbyId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get game."))
		return ctx, false
	}
	if game.Id == uuid.Nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("No game found."))
		return ctx, false
	}
	ctx.Game = game

	ctx.UserId = gsApi.GetUserId(r)
	if ctx.UserId == uuid.Nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to get user id."))
		return ctx, false
	}

	// A missing player comes back as the zero value with a nil error, so both
	// have to be checked.
	player, err := gsDatabase.GetLobbyUserPlayer(lobbyId, ctx.UserId)
	if err != nil || player.Id == uuid.Nil {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("You are not in this lobby."))
		return ctx, false
	}
	ctx.Player = player

	return ctx, true
}

func currentPlayerName(gameId uuid.UUID) string {
	players, err := database.GetPlayers(gameId)
	if err != nil {
		return ""
	}
	for _, player := range players {
		if player.IsCurrent {
			return player.UserName
		}
	}
	return ""
}

func turnOrderNames(gameId uuid.UUID) string {
	players, err := database.GetPlayers(gameId)
	if err != nil {
		return ""
	}
	names := make([]string, 0, len(players))
	for _, player := range players {
		if player.IsActive {
			names = append(names, player.UserName)
		}
	}
	return strings.Join(names, " → ")
}

// Create builds a lobby, its game, and the draw pile from the chosen decks and
// filters.
func Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	userId := gsApi.GetUserId(r)
	if userId == uuid.Nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to get user id."))
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("A lobby name is required."))
		return
	}

	cardsToWin, err := strconv.Atoi(strings.TrimSpace(r.FormValue("cardsToWin")))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Cards to win must be a whole number."))
		return
	}

	startingTokens := 2
	if raw := strings.TrimSpace(r.FormValue("startingTokens")); raw != "" {
		startingTokens, err = strconv.Atoi(raw)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Starting tokens must be a whole number."))
			return
		}
	}
	if err := database.ValidateStartingTokens(startingTokens); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(capitalize(err.Error())))
		return
	}

	deckIds, message := parseDeckIds(r.Form["deckId"], userId)
	if message != "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(message))
		return
	}

	ranges, message := parseYearRanges(r.Form["fromYear"], r.Form["toYear"])
	if message != "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(message))
		return
	}

	excluded, message := parseUUIDList(r.Form["excludedCategoryId"])
	if message != "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(message))
		return
	}

	// Check the pile is big enough before creating anything, so a rejected
	// setup does not leave an unplayable lobby lying around.
	total, err := database.CountCardsInDecksForRanges(deckIds, ranges, excluded)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to count matching songs."))
		return
	}
	if err := database.ValidateCardsToWin(cardsToWin, total); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(capitalize(err.Error()) + "."))
		return
	}

	lobbyId, err := database.CreateLobby(name, strings.TrimSpace(r.FormValue("message")), r.FormValue("password"))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to create lobby."))
		return
	}

	gameId, err := database.CreateGame(lobbyId, cardsToWin, startingTokens)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to create game."))
		return
	}

	for _, yearRange := range ranges {
		if err := database.AddYearRange(gameId, yearRange.FromYear, yearRange.ToYear); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to save era filter."))
			return
		}
	}

	if err := database.InitializeDrawPile(gameId, deckIds, excluded); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to build the draw pile."))
		return
	}
	if err := database.ApplyYearRangeFilter(gameId); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to apply era filters."))
		return
	}

	if err := gsDatabase.AddUserLobbyAccess(userId, lobbyId); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to grant lobby access."))
		return
	}

	w.Header().Add("HX-Redirect", "/track-timeline/"+lobbyId.String())
	w.WriteHeader(http.StatusCreated)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// parseDeckIds validates the selected decks and confirms the caller may read
// each one, so a lobby cannot be seeded from a deck its creator cannot open.
func parseDeckIds(values []string, userId uuid.UUID) ([]uuid.UUID, string) {
	if len(values) == 0 {
		return nil, "Select at least one deck."
	}

	deckIds := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		deckId, err := uuid.Parse(strings.TrimSpace(value))
		if err != nil {
			return nil, "Invalid deck."
		}
		ok, err := gsDatabase.UserHasDeckAccess(userId, deckId)
		if err != nil {
			return nil, "Failed to check deck access."
		}
		if !ok {
			return nil, "You do not have access to one of the selected decks."
		}
		deckIds = append(deckIds, deckId)
	}
	return deckIds, ""
}

// parseYearRanges reads the parallel from/to arrays the setup form posts.
func parseYearRanges(fromValues []string, toValues []string) ([]database.YearRange, string) {
	if len(fromValues) != len(toValues) {
		return nil, "Era filters are incomplete."
	}

	ranges := make([]database.YearRange, 0, len(fromValues))
	for i := range fromValues {
		fromRaw := strings.TrimSpace(fromValues[i])
		toRaw := strings.TrimSpace(toValues[i])
		if fromRaw == "" && toRaw == "" {
			continue
		}
		from, err := strconv.Atoi(fromRaw)
		if err != nil {
			return nil, "Era years must be whole numbers."
		}
		to, err := strconv.Atoi(toRaw)
		if err != nil {
			return nil, "Era years must be whole numbers."
		}
		if from > to {
			return nil, "An era's start year must not be after its end year."
		}
		ranges = append(ranges, database.YearRange{FromYear: from, ToYear: to})
	}
	return ranges, ""
}

func parseUUIDList(values []string) ([]uuid.UUID, string) {
	ids := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		id, err := uuid.Parse(value)
		if err != nil {
			return nil, "Invalid genre filter."
		}
		ids = append(ids, id)
	}
	return ids, ""
}

// CardCount powers the live "N songs match" estimate on the setup form.
func CardCount(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("0"))
		return
	}

	userId := gsApi.GetUserId(r)
	deckIds, message := parseDeckIds(r.Form["deckId"], userId)
	if message != "" {
		_, _ = w.Write([]byte("0"))
		return
	}
	ranges, message := parseYearRanges(r.Form["fromYear"], r.Form["toYear"])
	if message != "" {
		_, _ = w.Write([]byte("0"))
		return
	}
	excluded, message := parseUUIDList(r.Form["excludedCategoryId"])
	if message != "" {
		_, _ = w.Write([]byte("0"))
		return
	}

	count, err := database.CountCardsInDecksForRanges(deckIds, ranges, excluded)
	if err != nil {
		_, _ = w.Write([]byte("0"))
		return
	}

	_, _ = w.Write([]byte(strconv.Itoa(count)))
}

// Search renders the lobby list rows.
func Search(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	name := r.FormValue("name")
	page := 1
	if raw := strings.TrimSpace(r.FormValue("page")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			page = parsed
		}
	}

	rowCount, err := database.CountLobbies(name)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to count lobbies."))
		return
	}
	lastPage := int(math.Ceil(float64(rowCount) / 10))
	if lastPage < 1 {
		lastPage = 1
	}
	if page > lastPage {
		page = lastPage
	}

	lobbies, err := database.SearchLobbies(name, page)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to search lobbies."))
		return
	}

	tmpl, err := template.ParseFS(static.StaticFiles, "html/components/table-rows/lobby-rows.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse template."))
		return
	}

	type data struct {
		Lobbies  []database.LobbyDetails
		Page     int
		LastPage int
	}

	_ = tmpl.Execute(w, data{Lobbies: lobbies, Page: page, LastPage: lastPage})
}

// StartGame deals the opening hand and draws the first song.
func StartGame(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}

	if ctx.Game.GameStatus != database.StatusWaiting {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The game has already started."))
		return
	}

	if err := database.StartGame(ctx.Game.Id); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to start the game: " + err.Error()))
		return
	}

	announce(ctx.LobbyId, fmt.Sprintf("<blue>Game started</> — turn order: %s", esc(turnOrderNames(ctx.Game.Id))))
	gsWebsocket.LobbyBroadcast(ctx.LobbyId, "reload")

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Game started."))
}

// ResetGame returns a finished game to the lobby screen.
func ResetGame(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}

	if err := database.ResetGame(ctx.Game.Id); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to reset the game."))
		return
	}

	announce(ctx.LobbyId, "<blue>Game reset</> — ready for a new game")
	gsWebsocket.LobbyBroadcast(ctx.LobbyId, "reload")

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Game reset."))
}

// PlaySong cues the current song on every client. Playback is started by the
// player on turn rather than automatically on the previous round's reveal: it
// gives everyone time to read the answer, and it means a song never starts
// underneath a popup.
func PlaySong(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}

	if ctx.Game.GameStatus != database.StatusActive {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The game is not running."))
		return
	}
	if !ctx.Game.CurrentPlayerId.Valid || ctx.Game.CurrentPlayerId.UUID != ctx.Player.Id {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Only the player on turn can start the song."))
		return
	}

	card, err := database.GetCurrentCard(ctx.Game.Id)
	if err != nil || card.CardId == uuid.Nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("No song is in play."))
		return
	}

	sendSong(ctx.LobbyId, card)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Playing."))
}
