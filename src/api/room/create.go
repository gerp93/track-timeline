package apiRoom

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	gsApi "github.com/gerp93/gameshell-framework/api"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/guess"
)

const hostCookieName = "TRACK-TIMELINE-ROOM-HOST"
const guestNightCookieName = "TRACK-TIMELINE-ROOM-GUEST"

// Create starts a room-mode session: lobby + game + room row + host cookie,
// then redirects the creator's browser to the seatless host display. The
// creator still joins a seat from their phone separately.
func Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	userId := gsApi.GetUserId(r)
	if userId == uuid.Nil {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Log in to host a room."))
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = "Room Night"
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

	playbackMode := strings.TrimSpace(r.FormValue("playbackMode"))
	if playbackMode == "" {
		playbackMode = database.PlaybackSample
	}
	if err := database.ValidatePlaybackMode(playbackMode); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(capitalize(err.Error())))
		return
	}

	guessMode := strings.TrimSpace(r.FormValue("guessMode"))
	if guessMode == "" {
		guessMode = database.GuessModeBoth
	}
	if err := database.ValidateGuessMode(guessMode); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(capitalize(err.Error())))
		return
	}

	guessMatchPercent := database.DefaultGuessMatchPercent
	if raw := strings.TrimSpace(r.FormValue("guessMatchPercent")); raw != "" {
		guessMatchPercent, err = strconv.Atoi(raw)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Match required must be 60, 70, 80, or 90."))
			return
		}
	}
	if err := database.ValidateGuessMatchPercent(guessMatchPercent); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(capitalize(err.Error())))
		return
	}

	guessJudge := strings.TrimSpace(r.FormValue("guessJudge"))
	if guessJudge == "" {
		if guess.ClaudeConfigured() {
			guessJudge = database.GuessJudgeClaude
		} else {
			guessJudge = database.GuessJudgeLocal
		}
	}
	if err := database.ValidateGuessJudge(guessJudge); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(capitalize(err.Error())))
		return
	}
	if guessJudge == database.GuessJudgeClaude && !guess.ClaudeConfigured() {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Intent judging is not configured on this server."))
		return
	}

	clipSeconds := 20
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

	code, err := database.NewRoomCode()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to mint a room code."))
		return
	}
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := database.GetRoomByCode(code); err != nil {
			break
		}
		code, err = database.NewRoomCode()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to mint a room code."))
			return
		}
	}

	hostToken, err := database.NewHostToken()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to mint a host token."))
		return
	}

	lobbyName := fmt.Sprintf("%s [%s]", name, code)
	lobbyId, err := database.CreateLobby(lobbyName, "Room mode", "")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to create lobby."))
		return
	}

	gameId, err := database.CreateGame(lobbyId, cardsToWin, startingTokens, guessMode, guessMatchPercent, guessJudge, playbackMode, clipSeconds)
	if err != nil {
		_ = gsDatabase.DeleteLobby(lobbyId)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to create game."))
		return
	}

	for _, yearRange := range ranges {
		if err := database.AddYearRange(gameId, yearRange.FromYear, yearRange.ToYear); err != nil {
			_ = gsDatabase.DeleteLobby(lobbyId)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to save era filter."))
			return
		}
	}

	if err := database.InitializeDrawPile(gameId, deckIds, excluded); err != nil {
		_ = gsDatabase.DeleteLobby(lobbyId)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to build the draw pile."))
		return
	}
	if err := database.ApplyYearRangeFilter(gameId); err != nil {
		_ = gsDatabase.DeleteLobby(lobbyId)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to apply era filters."))
		return
	}

	if err := gsDatabase.AddUserLobbyAccess(userId, lobbyId); err != nil {
		_ = gsDatabase.DeleteLobby(lobbyId)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to grant lobby access."))
		return
	}

	if _, err := database.CreateRoom(lobbyId, userId, code, hostToken); err != nil {
		_ = gsDatabase.DeleteLobby(lobbyId)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to create room."))
		return
	}

	setHostCookie(w, code, hostToken)
	w.Header().Add("HX-Redirect", "/room/"+code+"/host")
	w.WriteHeader(http.StatusCreated)
}

func setHostCookie(w http.ResponseWriter, code string, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     hostCookieName,
		Value:    strings.ToUpper(code) + ":" + token,
		Path:     "/",
		// Long enough for an early-evening start through a late night.
		MaxAge:   16 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// setGuestNightCookie keeps a seat recoverable for the same room overnight even
// when the framework auth cookie (Secure + 12h HMAC) expires or was dropped on
// plain HTTP LAN phones. Rejoining with this cookie remints the auth session.
func setGuestNightCookie(w http.ResponseWriter, code string, userId uuid.UUID) {
	http.SetCookie(w, &http.Cookie{
		Name:     guestNightCookieName,
		Value:    strings.ToUpper(code) + ":" + userId.String(),
		Path:     "/",
		MaxAge:   16 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func readGuestNightUser(r *http.Request, code string) (uuid.UUID, bool) {
	c, err := r.Cookie(guestNightCookieName)
	if err != nil || c.Value == "" {
		return uuid.Nil, false
	}
	parts := strings.SplitN(c.Value, ":", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], code) {
		return uuid.Nil, false
	}
	userId, err := uuid.Parse(parts[1])
	if err != nil || userId == uuid.Nil {
		return uuid.Nil, false
	}
	return userId, true
}

func readHostToken(r *http.Request, code string) (string, bool) {
	c, err := r.Cookie(hostCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	parts := strings.SplitN(c.Value, ":", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], code) {
		return "", false
	}
	return parts[1], true
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

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
		fromYear, err := strconv.Atoi(fromRaw)
		if err != nil {
			return nil, "Era start year is invalid."
		}
		toYear, err := strconv.Atoi(toRaw)
		if err != nil {
			return nil, "Era end year is invalid."
		}
		if fromYear > toYear {
			return nil, "Era start must be before end."
		}
		ranges = append(ranges, database.YearRange{FromYear: fromYear, ToYear: toYear})
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

func randomPassword() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
