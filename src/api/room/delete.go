package apiRoom

import (
	"net/http"
	"strings"

	gsApi "github.com/gerp93/gameshell-framework/api"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
)

// Delete removes a room and its lobby. Only the account that created the room
// (or an admin) may, since the host screen is seatless and has no player row
// for the ordinary lobby delete to check.
func Delete(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	room, err := database.GetRoomByCode(code)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("No room with that code."))
		return
	}

	userId := gsApi.GetUserId(r)
	if userId == uuid.Nil || (userId != room.CreatorUserId && !gsApi.UserIsAdmin(r)) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Only the room's host can delete it."))
		return
	}

	// Tell any open phone or host screen first; the lobby is gone after this.
	Broadcast(room.LobbyId, "kick")
	if err := gsDatabase.DeleteLobby(room.LobbyId); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to delete the room."))
		return
	}

	// Re-run the page so the list reflects the deletion.
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Deleted."))
}
