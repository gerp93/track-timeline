package apiRoom

import (
	"log"
	"net/http"
	"strings"

	gsAuth "github.com/gerp93/gameshell-framework/auth"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
)

// ServeWs upgrades a room client. Hosts authenticate with the host cookie;
// seats authenticate with the normal user cookie and must already be seated.
// This handler is not wrapped in MiddlewareForAPIs, so seats read the auth
// cookie directly rather than the API request-context user id.
func ServeWs(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	room, err := database.GetRoomByCode(code)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Room not found."))
		return
	}

	role := strings.TrimSpace(r.URL.Query().Get("role"))
	var userId uuid.UUID

	switch role {
	case RoleHost:
		token, ok := readHostToken(r, room.Code)
		if !ok || token != room.HostToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("Host token missing or invalid."))
			return
		}
		userId = room.CreatorUserId
	case RoleSeat:
		userId, err = gsAuth.GetUserId(r)
		if err != nil || userId == uuid.Nil {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("Not logged in."))
			return
		}
		player, playerErr := gsDatabase.GetLobbyUserPlayer(room.LobbyId, userId)
		if playerErr != nil || player.Id == uuid.Nil {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("You are not seated in this room."))
			return
		}
	default:
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("role must be host or seat."))
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	h := getHub(room.LobbyId)
	client := &client{
		hub:    h,
		conn:   conn,
		send:   make(chan []byte, 32),
		role:   role,
		userId: userId,
	}
	h.register <- client
	go client.writePump()
	client.readPump()
}
