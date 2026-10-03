package apiRoom

import (
	"html"
	"net/http"
	"strconv"
	"strings"

	gsApi "github.com/gerp93/gameshell-framework/api"

	"github.com/gerp93/track-timeline/database"
)

// Players renders who has sat down in a room, for the waiting phones to check
// before anyone starts the game. It only lists names, which anyone holding the
// room code could see by joining anyway.
func Players(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	room, err := database.GetRoomByCode(code)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("No room with that code."))
		return
	}
	game, err := database.GetGame(room.LobbyId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to load game."))
		return
	}
	players, err := database.GetPlayers(game.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to load players."))
		return
	}

	viewer := gsApi.GetUserId(r)
	var items strings.Builder
	count := 0
	for _, player := range players {
		if !player.IsActive {
			continue
		}
		count++
		items.WriteString("<li>")
		items.WriteString(html.EscapeString(database.GuestDisplayName(player.UserName)))
		if player.UserId == viewer {
			items.WriteString(` <span class="room-phone-players-you">(you)</span>`)
		}
		items.WriteString("</li>")
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if count == 0 {
		_, _ = w.Write([]byte(`<p class="hint">Nobody has joined yet.</p>`))
		return
	}
	_, _ = w.Write([]byte(`<h3 class="room-phone-players-title">Joined (` + strconv.Itoa(count) + `)</h3><ul class="room-phone-players-list">` + items.String() + `</ul>`))
}
