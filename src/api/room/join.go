package apiRoom

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	gsApi "github.com/gerp93/gameshell-framework/api"
	gsAuth "github.com/gerp93/gameshell-framework/auth"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
)

// JoinGuest seats a couch guest: mint or reuse a synthetic USER, set the auth
// cookie, grant lobby access, and sit them in the lobby.
func JoinGuest(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	room, err := database.GetRoomByCode(code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("No room with that code."))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to look up room."))
		return
	}

	displayName, err := database.ValidateGuestDisplayName(r.FormValue("name"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(capitalize(err.Error()) + "."))
		return
	}

	userName := database.GuestUserName(displayName, room.Code)
	userId, err := gsDatabase.GetUserIdByName(userName)
	if err != nil || userId == uuid.Nil {
		if err := gsDatabase.CreateUser(userName, randomPassword(), true); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to create guest seat."))
			return
		}
		userId, err = gsDatabase.GetUserIdByName(userName)
		if err != nil || userId == uuid.Nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to resolve guest seat."))
			return
		}
	}

	if err := seatUserInRoom(room, userId); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(err.Error()))
		return
	}

	gsAuth.SetUserId(w, userId)
	setGuestNightCookie(w, room.Code, userId)
	Broadcast(room.LobbyId, "log:"+displayName+" sat down")
	Broadcast(room.LobbyId, "refresh")
	w.Header().Add("HX-Redirect", "/room/"+room.Code+"/play")
	w.WriteHeader(http.StatusOK)
}

// JoinAccount seats the already-logged-in user into the room.
func JoinAccount(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	room, err := database.GetRoomByCode(code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("No room with that code."))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to look up room."))
		return
	}

	userId := gsApi.GetUserId(r)
	if userId == uuid.Nil {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Log in first, or join as a guest."))
		return
	}

	if err := seatUserInRoom(room, userId); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(err.Error()))
		return
	}

	user, err := gsDatabase.GetUser(userId)
	name := "Player"
	if err == nil {
		name = user.Name
	}
	gsAuth.SetUserId(w, userId)
	setGuestNightCookie(w, room.Code, userId)
	Broadcast(room.LobbyId, "log:"+name+" sat down")
	Broadcast(room.LobbyId, "refresh")
	w.Header().Add("HX-Redirect", "/room/"+room.Code+"/play")
	w.WriteHeader(http.StatusOK)
}

func seatUserInRoom(room database.Room, userId uuid.UUID) error {
	if err := gsDatabase.AddUserLobbyAccess(userId, room.LobbyId); err != nil {
		return errors.New("Failed to grant room access.")
	}
	if _, err := gsDatabase.AddUserToLobby(room.LobbyId, userId); err != nil {
		return errors.New("Failed to sit down.")
	}
	return nil
}
