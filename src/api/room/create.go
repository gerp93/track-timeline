package apiRoom

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
)

const hostCookieName = "TRACK-TIMELINE-ROOM-HOST"
const guestNightCookieName = "TRACK-TIMELINE-ROOM-GUEST"

// MintCode returns a room code that no existing room is using.
func MintCode() (string, error) {
	code, err := database.NewRoomCode()
	if err != nil {
		return "", err
	}
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := database.GetRoomByCode(code); err != nil {
			break
		}
		if code, err = database.NewRoomCode(); err != nil {
			return "", err
		}
	}
	return code, nil
}

// Finish turns a freshly built lobby into a room: it records the room row and
// host token, sets the host cookie, and sends the creator's browser to the
// seatless host display. The creator still joins a seat from their phone
// separately. It writes its own error response and returns false on failure,
// leaving the caller to clean up the lobby it built.
func Finish(w http.ResponseWriter, lobbyId uuid.UUID, userId uuid.UUID, code string) bool {
	hostToken, err := database.NewHostToken()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to mint a host token."))
		return false
	}
	if _, err := database.CreateRoom(lobbyId, userId, code, hostToken); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to create room."))
		return false
	}

	setHostCookie(w, code, hostToken)
	w.Header().Add("HX-Redirect", "/room/"+code+"/host")
	w.WriteHeader(http.StatusCreated)
	return true
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

func randomPassword() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
