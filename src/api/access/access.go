package apiAccess

import (
	"net/http"

	gsApi "github.com/gerp93/gameshell-framework/api"
	gsAuth "github.com/gerp93/gameshell-framework/auth"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	"github.com/google/uuid"
)

// Lobby grants access to a password-protected lobby.
func Lobby(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	lobbyId, err := uuid.Parse(r.PathValue("lobbyId"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid lobby."))
		return
	}

	userId := gsApi.GetUserId(r)
	if userId == uuid.Nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to get user id."))
		return
	}

	passwordHash, err := gsDatabase.GetLobbyPasswordHash(lobbyId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check the lobby password."))
		return
	}

	// A lobby with no password is open; anyone asking simply gets access.
	if passwordHash.Valid && !gsAuth.PasswordMatchesHash(r.FormValue("password"), passwordHash.String) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Incorrect password."))
		return
	}

	if err := gsDatabase.AddUserLobbyAccess(userId, lobbyId); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to grant access."))
		return
	}

	w.Header().Add("HX-Redirect", "/track-timeline/"+lobbyId.String())
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Access granted."))
}

// Deck grants access to a password-protected deck.
func Deck(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	deckId, err := uuid.Parse(r.PathValue("deckId"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid deck."))
		return
	}

	userId := gsApi.GetUserId(r)
	if userId == uuid.Nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to get user id."))
		return
	}

	passwordHash, err := gsDatabase.GetDeckPasswordHash(deckId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check the deck password."))
		return
	}

	if !gsAuth.PasswordMatchesHash(r.FormValue("password"), passwordHash) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Incorrect password."))
		return
	}

	if err := gsDatabase.AddUserDeckAccess(userId, deckId); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to grant access."))
		return
	}

	w.Header().Add("HX-Redirect", "/deck/"+deckId.String())
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Access granted."))
}
