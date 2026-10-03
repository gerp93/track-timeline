package apiRoom

import (
	"net/http"
	"strings"

	gsAuth "github.com/gerp93/gameshell-framework/auth"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
)

// guestRoom reports whether the request carries a room guest's session and, if
// so, which room that guest sat down in. A guest is an unapproved account:
// JoinGuest creates them that way, and approval is only enforced by the
// password login, which a guest never goes through. Nothing else in the app
// distinguishes them from a real signed-in user, so without RestrictGuests a
// guest could open the lobby list, make lobbies, edit decks and cards, or
// change their own password.
func guestRoom(r *http.Request) (room database.Room, isGuest bool, hasRoom bool) {
	userId, err := gsAuth.GetUserId(r)
	if err != nil || userId == uuid.Nil {
		return room, false, false
	}
	approved, err := gsDatabase.GetUserIsApproved(userId)
	if err != nil || approved {
		return room, false, false
	}

	// The room the guest sat down in is the one their guest-night cookie names
	// ("CODE:userId"), and it only counts if the cookie is theirs.
	cookie, err := r.Cookie(guestNightCookieName)
	if err != nil {
		return room, true, false
	}
	code, cookieUser, found := strings.Cut(cookie.Value, ":")
	if !found || cookieUser != userId.String() {
		return room, true, false
	}
	room, err = database.GetRoomByCode(code)
	return room, true, err == nil
}

// RestrictGuests confines a room guest's session to their own room: its pages,
// its websocket, the per-lobby game endpoints for that room's lobby, sign-out
// and choosing a colour theme. Everything else is refused, and a guest who wanders to another
// page is sent back to their room. Requests without a guest session pass
// through untouched.
func RestrictGuests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		room, isGuest, hasRoom := guestRoom(r)
		if !isGuest {
			next.ServeHTTP(w, r)
			return
		}

		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/room/"),
			strings.HasPrefix(path, "/api/room/"),
			strings.HasPrefix(path, "/ws/room/"),
			strings.HasPrefix(path, "/static/"),
			strings.HasPrefix(path, "/gs/"),
			path == "/api/user/logout",
			// The one account setting a guest gets. The handler itself only
			// lets a user change their own theme.
			strings.HasPrefix(path, "/api/user/") && strings.HasSuffix(path, "/color-theme") && r.Method == http.MethodPut,
			// The win/lose celebration popup shows other players' images, which
			// it loads from here (read-only).
			strings.HasPrefix(path, "/api/user/") && (strings.HasSuffix(path, "/win-gif") || strings.HasSuffix(path, "/lose-gif")) && r.Method == http.MethodGet:
			next.ServeHTTP(w, r)
			return
		}

		// Per-lobby game endpoints and the lobby websocket: only for the
		// guest's own room's lobby.
		if hasRoom {
			for _, prefix := range []string{"/api/track-timeline/", "/ws/lobby/"} {
				if rest, ok := strings.CutPrefix(path, prefix); ok {
					lobbyId, _, _ := strings.Cut(rest, "/")
					if lobbyId == room.LobbyId.String() {
						next.ServeHTTP(w, r)
						return
					}
				}
			}
		}

		if r.Method == http.MethodGet && !strings.HasPrefix(path, "/api/") && !strings.HasPrefix(path, "/ws/") && hasRoom {
			http.Redirect(w, r, "/room/"+room.Code+"/play", http.StatusSeeOther)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Room guests can only play in their room."))
	})
}
