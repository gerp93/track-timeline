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

	apiRoom "github.com/gerp93/track-timeline/api/room"
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

	// Game-win YouTube clip (account Win Video). Empty when the winner has
	// none configured — clients fall back to the normal celebration popup.
	WinVideoId           string `json:"winVideoId,omitempty"`
	WinVideoStartSeconds int    `json:"winVideoStartSeconds,omitempty"`

	// Guess-token outcome, independent of the card outcome above. Every
	// qualifying guess earns its own token -- no single-token race -- so this
	// is a list, one entry per player who named the song this round.
	GuessTokenWinners []guessTokenWinnerPayload `json:"guessTokenWinners,omitempty"`
}

type guessTokenWinnerPayload struct {
	Name      string `json:"name"`
	GuessText string `json:"guessText,omitempty"`
	Tokens    int    `json:"tokens"`
}

// songPayload tells every client which song to cue and which slice of it to
// play. EndSeconds of 0 means "play to the end of the video" (the 'full'
// playback mode); anything else stops the clip there, which the IFrame API
// does natively via loadVideoById's own endSeconds.
type songPayload struct {
	VideoId      string `json:"videoId"`
	StartSeconds int    `json:"startSeconds"`
	EndSeconds   int    `json:"endSeconds,omitempty"`
}

// esc escapes text bound for a chat line. The framework only escapes messages
// arriving from players over the socket, and the shared chat renderer writes
// with innerHTML, so anything the server interpolates has to be escaped here.
func esc(s string) string {
	return html.EscapeString(s)
}

// tokensWonLost is the chat phrasing for a token-balance change: "won 1 token",
// "lost 2 tokens". Always include the count so a wager of 3 reads the same
// way as a skip of 1.
func tokensWonLost(delta int) string {
	n := delta
	if n < 0 {
		n = -n
	}
	word := "token"
	if n != 1 {
		word = "tokens"
	}
	if delta >= 0 {
		return fmt.Sprintf("won %d %s", n, word)
	}
	return fmt.Sprintf("lost %d %s", n, word)
}

// wagerResult is the chat phrasing for a settled exact-year wager: it names the
// stake as well as the outcome ("wagered 3 tokens and lost 3 tokens"), since
// the won/lost amount alone does not tell the table how much was riding on it.
func wagerResult(wager int, correct bool) string {
	delta := -wager
	if correct {
		delta = wager
	}
	return fmt.Sprintf("wagered %s and %s", tokenCount(wager), tokensWonLost(delta))
}

// tokenCount phrases a number of tokens for chat: "1 token", "2 tokens".
func tokenCount(n int) string {
	return database.CurrentEconomy().Tokens(n)
}

// tokensSpent is tokensWonLost's counterpart for a deliberate purchase: a
// player who pays for something chose to spend, they did not "lose" tokens.
func tokensSpent(n int) string {
	word := "token"
	if n != 1 {
		word = "tokens"
	}
	return fmt.Sprintf("spent %d %s", n, word)
}

// announce posts a chat line to the lobby. A bare string with no prefix is
// rendered as chat.
func announce(lobbyId uuid.UUID, message string) {
	gsWebsocket.LobbyBroadcast(lobbyId, message)
	apiRoom.MirrorBroadcast(lobbyId, "log:"+message)
	apiRoom.MirrorBroadcast(lobbyId, message)
}

// chatDivider is a plain line of dashes marking the end of one turn's chat
// lines (guesses, tokens earned, the placement/steal verdict) before the
// next turn's start their own cluster — the shared chat renderer
// (gameshell-framework's chat.js) only understands plain text plus the
// <red>/<green>/<blue> color tokens, so this is deliberately just
// characters, not a real HTML rule.
const chatDivider = "──────────────────────────"

// announceDivider posts the turn-boundary divider to the lobby.
func announceDivider(lobbyId uuid.UUID) {
	announce(lobbyId, chatDivider)
}

// sendStatus updates the bottom status line for everyone, with no popup.
func sendStatus(lobbyId uuid.UUID, message string) {
	gsWebsocket.LobbyBroadcast(lobbyId, "status:"+message)
	apiRoom.MirrorBroadcast(lobbyId, "status:"+message)
}

// sendResult drives the reveal popup and the status line together.
func sendResult(lobbyId uuid.UUID, payload resultPayload) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		log.Println(err)
		return
	}
	gsWebsocket.LobbyBroadcast(lobbyId, "result:"+string(encoded))
	apiRoom.MirrorBroadcast(lobbyId, "result:"+string(encoded))
}

// sendSong cues the same song, over the same window, on every client. The
// window is resolved once here and broadcast, rather than each client
// deciding for itself, so a random sample is the same random sample for
// everyone in the lobby.
func sendSong(lobbyId uuid.UUID, card database.CurrentCard, window database.ClipWindow) {
	encoded, err := json.Marshal(songPayload{
		VideoId:      card.YouTubeVideoId,
		StartSeconds: window.StartSeconds,
		EndSeconds:   window.EndSeconds,
	})
	if err != nil {
		log.Println(err)
		return
	}
	gsWebsocket.LobbyBroadcast(lobbyId, "song:"+string(encoded))
	apiRoom.MirrorBroadcast(lobbyId, "song:"+string(encoded))
}

func refresh(lobbyId uuid.UUID) {
	gsWebsocket.LobbyBroadcast(lobbyId, "refresh")
	apiRoom.MirrorBroadcast(lobbyId, "refresh")
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

	if room, roomErr := database.GetRoomByLobbyId(lobbyId); roomErr == nil && room.IsPaused {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("The room is paused until the host display reconnects."))
			return ctx, false
		}
	}

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

// userIdForPlayer maps a seat to the USER row that owns celebration media.
func userIdForPlayer(gameId uuid.UUID, playerId uuid.UUID) uuid.UUID {
	players, err := database.GetPlayers(gameId)
	if err != nil {
		return uuid.Nil
	}
	for _, player := range players {
		if player.PlayerId == playerId {
			return player.UserId
		}
	}
	return uuid.Nil
}

// winCelebrationFor loads a player's personalized win GIF/message, if they set
// one. Failures are non-fatal — the popup just falls back to its plain form.
func winCelebrationFor(userId uuid.UUID) (celebration string, hasGif bool) {
	c, err := gsDatabase.GetUserWinCelebration(userId)
	if err != nil {
		log.Println(err)
		return "", false
	}
	return c.Message.String, c.HasGif
}

// loseCelebrationFor is winCelebrationFor's counterpart for the player who
// just missed a placement (the discarded-round case). The shared "nobody got
// it" popup still carries one person's commiseration — the turn player's —
// matching timeline-trivia's incorrect-guess path.
func loseCelebrationFor(userId uuid.UUID) (celebration string, hasGif bool) {
	c, err := gsDatabase.GetUserLoseCelebration(userId)
	if err != nil {
		log.Println(err)
		return "", false
	}
	return c.Message.String, c.HasGif
}

// winVideoFor loads a player's game-win YouTube clip, if they set one.
// Failures are non-fatal — game-over just skips the forced video.
func winVideoFor(userId uuid.UUID) (videoId string, startSeconds int) {
	v, err := gsDatabase.GetUserWinVideo(userId)
	if err != nil {
		log.Println(err)
		return "", 0
	}
	if !v.HasVideo {
		return "", 0
	}
	return v.YouTubeVideoId.String, v.StartOffsetSeconds
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

	startingTokens := database.DefaultStartingTokens
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

	playbackMode := strings.TrimSpace(r.FormValue("playbackMode"))
	if playbackMode == "" {
		playbackMode = database.PlaybackSample
	}
	if err := database.ValidatePlaybackMode(playbackMode); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(capitalize(err.Error())))
		return
	}

	// A select on the form ("1" = never-played songs first); anything else,
	// including a missing field from an older client, is the plain random pile.
	freshSongsFirst := strings.TrimSpace(r.FormValue("freshSongsFirst")) == "1"

	clipSeconds := 30
	if raw := strings.TrimSpace(r.FormValue("clipSeconds")); raw != "" {
		clipSeconds, err = strconv.Atoi(raw)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Clip length must be a whole number of seconds."))
			return
		}
	}
	if err := database.ValidateClipSeconds(clipSeconds); err != nil {
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

	gameId, err := database.CreateGame(lobbyId, cardsToWin, startingTokens, freshSongsFirst, playbackMode, clipSeconds)
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
// UserCanReadDeck (not UserHasDeckAccess) is deliberate: this is a read/use
// check, not an edit check, so a deck flagged public-readonly must pass too.
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
		ok, err := gsDatabase.UserCanReadDeck(userId, deckId)
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

// DeleteLobby removes a lobby nobody's using anymore -- an explicit cleanup
// action for one that never started or has already finished, rather than
// waiting on the framework's own delete-when-empty behavior
// (gameshell-framework/websocket/hub.go), which only fires once every last
// connected client actually disconnects and can leave a finished lobby
// sitting in the list indefinitely until that happens. loadContext already
// requires the caller to have a PLAYER row in this lobby, so only someone
// who was actually part of it can delete it.
func DeleteLobby(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if ctx.Game.GameStatus == database.StatusActive {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("You can't delete a lobby with a game in progress."))
		return
	}

	if err := gsDatabase.DeleteLobby(ctx.LobbyId); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to delete the lobby."))
		return
	}
	// Best-effort: anyone who still has this lobby's own page open elsewhere
	// gets sent back to the list rather than left looking at a dead page.
	gsWebsocket.LobbyBroadcast(ctx.LobbyId, "kick")

	// A full refresh (rather than an htmx row swap) re-runs the lobbies
	// search so the list, its pagination, and the deleted row all stay
	// consistent with one re-fetch instead of hand-patching the DOM.
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Deleted."))
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
	apiRoom.MirrorBroadcast(ctx.LobbyId, "reload")

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
	apiRoom.MirrorBroadcast(ctx.LobbyId, "reload")

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
	if challengeInProgress(w, ctx) {
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

	// The window was chosen when the card was drawn (see database.DrawCard).
	// If sample mode fell back to an intro window at draw because duration
	// was unknown, and a measured length is available now, re-roll once into
	// a real middle sample and stamp it so replay stays on that same clip.
	window := database.ClipWindow{
		StartSeconds: ctx.Game.ClipStartSeconds,
		EndSeconds:   ctx.Game.ClipEndSeconds,
	}
	if ctx.Game.PlaybackMode == database.PlaybackSample &&
		database.SampleWouldFit(ctx.Game.ClipSeconds, card.DurationSeconds) &&
		window.StartSeconds == 0 && window.EndSeconds == ctx.Game.ClipSeconds {
		window = database.ResolveClipWindow(ctx.Game.PlaybackMode, ctx.Game.ClipSeconds, card.DurationSeconds)
		if err := database.SetClipWindow(ctx.Game.Id, window); err != nil {
			log.Println(err)
		}
	}

	sendSong(ctx.LobbyId, card, window)

	// The song is on: the window for challenging the previous round is over.
	// Everyone's current-card fragment is refreshed so the Challenge button goes.
	if ctx.Game.BetweenRounds {
		if err := database.SetBetweenRounds(ctx.Game.Id, false); err != nil {
			log.Println(err)
		}
		refresh(ctx.LobbyId)
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Playing."))
}

// ReplaySong plays this round's clip again, over the exact same window, for
// one token. Available to the player on turn once per round, after they have
// heard it through or paused it — the point is a second listen, not a way to
// keep the song running while they think.
func ReplaySong(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if challengeInProgress(w, ctx) {
		return
	}

	if ctx.Game.GameStatus != database.StatusActive {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The game is not running."))
		return
	}
	if ctx.Game.RoundPhase != database.PhaseListening {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("It is too late to replay this song."))
		return
	}
	if !ctx.Game.CurrentPlayerId.Valid || ctx.Game.CurrentPlayerId.UUID != ctx.Player.Id {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Only the player on turn can replay the song."))
		return
	}
	if ctx.Game.ReplayUsed {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("You have already replayed this song."))
		return
	}

	tokens, err := database.GetPlayerTokens(ctx.Game.Id, ctx.Player.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check your tokens."))
		return
	}
	if tokens < database.ReplayCost {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("You need " + tokenCount(database.ReplayCost) + " to hear it again."))
		return
	}

	card, err := database.GetCurrentCard(ctx.Game.Id)
	if err != nil || card.CardId == uuid.Nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("No song is in play."))
		return
	}

	if _, err := database.AddPlayerTokens(ctx.Game.Id, ctx.Player.Id, -database.ReplayCost); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to spend your tokens."))
		return
	}
	if err := database.SetReplayUsed(ctx.Game.Id, true); err != nil {
		log.Println(err)
	}

	announce(ctx.LobbyId, fmt.Sprintf("<blue>%s</> %s to hear it again", esc(ctx.Player.Name), tokensSpent(database.ReplayCost)))
	sendSong(ctx.LobbyId, card, database.ClipWindow{
		StartSeconds: ctx.Game.ClipStartSeconds,
		EndSeconds:   ctx.Game.ClipEndSeconds,
	})
	refresh(ctx.LobbyId)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Replaying."))
}

// NewClip spends tokens to hear a different slice of this song instead of the
// one already heard — the same song, a fresh random window, as often as the
// player can pay. Gated like ReplaySong (the player on turn, before they lock
// in a placement) but with no once-a-round limit, and it never reveals which
// part of the song the new window is. Pointless when the lobby plays the whole
// song, so that mode refuses it.
func NewClip(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if challengeInProgress(w, ctx) {
		return
	}

	if ctx.Game.GameStatus != database.StatusActive {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The game is not running."))
		return
	}
	if ctx.Game.RoundPhase != database.PhaseListening {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("It is too late to pick another clip."))
		return
	}
	if !ctx.Game.CurrentPlayerId.Valid || ctx.Game.CurrentPlayerId.UUID != ctx.Player.Id {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Only the player on turn can pick another clip."))
		return
	}
	if ctx.Game.PlaybackMode == database.PlaybackFull {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The whole song already plays in this lobby."))
		return
	}

	tokens, err := database.GetPlayerTokens(ctx.Game.Id, ctx.Player.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check your tokens."))
		return
	}
	if tokens < database.NewClipCost {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("You need " + tokenCount(database.NewClipCost) + " to hear a different clip."))
		return
	}

	card, err := database.GetCurrentCard(ctx.Game.Id)
	if err != nil || card.CardId == uuid.Nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("No song is in play."))
		return
	}

	// Picked before any token moves, so a song with nothing else to offer
	// costs nothing.
	window, found := database.PickDifferentClipWindow(
		ctx.Game.ClipSeconds, card.DurationSeconds,
		database.ClipWindow{StartSeconds: ctx.Game.ClipStartSeconds, EndSeconds: ctx.Game.ClipEndSeconds},
	)
	if !found {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("There is no other clip to play for this song."))
		return
	}

	if _, err := database.AddPlayerTokens(ctx.Game.Id, ctx.Player.Id, -database.NewClipCost); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to spend your tokens."))
		return
	}
	if err := database.SetClipWindow(ctx.Game.Id, window); err != nil {
		log.Println(err)
	}

	announce(ctx.LobbyId, fmt.Sprintf("<blue>%s</> %s to hear a different clip", esc(ctx.Player.Name), tokensSpent(database.NewClipCost)))
	sendSong(ctx.LobbyId, card, window)
	refresh(ctx.LobbyId)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Playing a different clip."))
}

// PauseSong and ResumeSong toggle playback in place for everyone, distinct
// from PlaySong: PlaySong (re)cues the song from its configured start offset,
// while resuming has to continue from wherever playback was paused instead of
// restarting. Neither touches any game state — play/pause is ephemeral
// client-side UI, not something the round outcome depends on — so both are
// just a control-string broadcast, gated the same way PlaySong is.

func PauseSong(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if challengeInProgress(w, ctx) {
		return
	}

	if ctx.Game.GameStatus != database.StatusActive {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The game is not running."))
		return
	}
	if !ctx.Game.CurrentPlayerId.Valid || ctx.Game.CurrentPlayerId.UUID != ctx.Player.Id {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Only the player on turn can pause the song."))
		return
	}

	gsWebsocket.LobbyBroadcast(ctx.LobbyId, "songPause")
	apiRoom.MirrorBroadcast(ctx.LobbyId, "songPause")

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Paused."))
}

func ResumeSong(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if challengeInProgress(w, ctx) {
		return
	}

	if ctx.Game.GameStatus != database.StatusActive {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("The game is not running."))
		return
	}
	if !ctx.Game.CurrentPlayerId.Valid || ctx.Game.CurrentPlayerId.UUID != ctx.Player.Id {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Only the player on turn can resume the song."))
		return
	}

	gsWebsocket.LobbyBroadcast(ctx.LobbyId, "songResume")
	apiRoom.MirrorBroadcast(ctx.LobbyId, "songResume")

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Resumed."))
}

// settingsPayload is what a "settings:" message carries: the lobby settings
// that can change after the game has started, so every open page can follow.
type settingsPayload struct {
	PlaybackMode    string `json:"playbackMode"`
	ClipSeconds     int    `json:"clipSeconds"`
	FreshSongsFirst bool   `json:"freshSongsFirst"`
}

func sendSettings(lobbyId uuid.UUID, playbackMode string, clipSeconds int, freshSongsFirst bool) {
	encoded, err := json.Marshal(settingsPayload{
		PlaybackMode:    playbackMode,
		ClipSeconds:     clipSeconds,
		FreshSongsFirst: freshSongsFirst,
	})
	if err != nil {
		log.Println(err)
		return
	}
	gsWebsocket.LobbyBroadcast(lobbyId, "settings:"+string(encoded))
}

// UpdateSettings changes the lobby settings that are safe to change once the
// game is under way: how much of each song plays, and whether never-played songs
// are dealt first. Anyone in the lobby may, the same as the turn timer and the
// lobby message. A change applies from the next song: the clip window for the
// song in play is stamped when it is first played (see database.ClipWindow), so
// a replay of it stays the same clip. Fields left out keep their current value.
func UpdateSettings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := loadContext(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	playbackMode := ctx.Game.PlaybackMode
	if raw := strings.TrimSpace(r.FormValue("playbackMode")); raw != "" {
		playbackMode = raw
	}
	if err := database.ValidatePlaybackMode(playbackMode); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(capitalize(err.Error()) + "."))
		return
	}

	clipSeconds := ctx.Game.ClipSeconds
	if raw := strings.TrimSpace(r.FormValue("clipSeconds")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Clip length must be a whole number of seconds."))
			return
		}
		clipSeconds = parsed
	}
	if err := database.ValidateClipSeconds(clipSeconds); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(capitalize(err.Error()) + "."))
		return
	}

	freshSongsFirst := ctx.Game.FreshSongsFirst
	if _, sent := r.Form["freshSongsFirst"]; sent {
		freshSongsFirst = strings.TrimSpace(r.FormValue("freshSongsFirst")) == "1"
	}

	var changes []string
	if playbackMode != ctx.Game.PlaybackMode {
		changes = append(changes, "playback to “"+database.PlaybackLabel(playbackMode)+"”")
	}
	if clipSeconds != ctx.Game.ClipSeconds {
		changes = append(changes, fmt.Sprintf("clip length to %d seconds", clipSeconds))
	}
	if freshSongsFirst != ctx.Game.FreshSongsFirst {
		state := "off"
		if freshSongsFirst {
			state = "on"
		}
		changes = append(changes, "never-played songs first to "+state)
	}
	if len(changes) == 0 {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Nothing changed."))
		return
	}

	if err := database.UpdateGameSettings(ctx.Game.Id, playbackMode, clipSeconds, freshSongsFirst); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to save the settings."))
		return
	}
	// The pile's order is fixed when it is shuffled, so flipping the setting
	// only takes effect once the cards still in it are shuffled again.
	if freshSongsFirst != ctx.Game.FreshSongsFirst {
		if err := database.ShuffleDrawPile(ctx.Game.Id); err != nil {
			log.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Saved, but failed to reorder the draw pile."))
			return
		}
	}

	announce(ctx.LobbyId, fmt.Sprintf("<blue>%s</> changed %s", esc(ctx.Player.Name), joinPhrases(changes)))
	sendSettings(ctx.LobbyId, playbackMode, clipSeconds, freshSongsFirst)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Saved. It applies from the next song."))
}

// joinPhrases reads a list aloud: "a", "a and b", "a, b and c".
func joinPhrases(phrases []string) string {
	switch len(phrases) {
	case 0:
		return ""
	case 1:
		return phrases[0]
	}
	return strings.Join(phrases[:len(phrases)-1], ", ") + " and " + phrases[len(phrases)-1]
}
