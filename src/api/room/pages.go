package apiRoom

import (
	"database/sql"
	"errors"
	"html/template"
	"net/http"
	"strings"

	gsApi "github.com/gerp93/gameshell-framework/api"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	gsStatic "github.com/gerp93/gameshell-framework/static"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/static"
)

func parseChrome(bodyPattern string) (*template.Template, error) {
	t, err := template.New("base.html").ParseFS(gsStatic.StaticFiles, "html/pages/base.html")
	if err != nil {
		return nil, err
	}
	return t.ParseFS(static.StaticFiles, bodyPattern)
}

// CreatePage is the logged-in "host a room" setup form.
func CreatePage(w http.ResponseWriter, r *http.Request) {
	base := gsApi.GetBasePageData(r)
	base.PageTitle = "Host a Room"

	decks, err := gsDatabase.GetReadableDecks(base.User.Id)
	if err != nil {
		decks = nil
	}
	categories, err := database.GetCategories()
	if err != nil {
		categories = nil
	}

	tmpl, err := parseChrome("html/pages/body/room-create.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
		Decks      []gsDatabase.Deck
		Categories []database.Category
	}
	_ = tmpl.ExecuteTemplate(w, "base", data{
		BasePageData: base,
		Decks:        decks,
		Categories:   categories,
	})
}

// JoinPage is the public phone landing: guest name or account join.
func JoinPage(w http.ResponseWriter, r *http.Request) {
	base := gsApi.GetBasePageData(r)
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
	base.PageTitle = "Join " + room.Code

	tmpl, err := parseChrome("html/pages/body/room-join.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
		Room database.Room
	}
	_ = tmpl.ExecuteTemplate(w, "base", data{BasePageData: base, Room: room})
}

// HostPage is the seatless TV/laptop display.
func HostPage(w http.ResponseWriter, r *http.Request) {
	base := gsApi.GetBasePageData(r)
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	room, err := database.GetRoomByCode(code)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("No room with that code."))
		return
	}

	token, ok := readHostToken(r, room.Code)
	if !ok || token != room.HostToken {
		// Creator can re-mint the host cookie by visiting while logged in.
		userId := base.User.Id
		if userId != uuid.Nil && userId == room.CreatorUserId {
			setHostCookie(w, room.Code, room.HostToken)
		} else {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("This display is not authorized as the room host."))
			return
		}
	}

	game, err := database.GetGame(room.LobbyId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to load game."))
		return
	}

	base.PageTitle = "Room " + room.Code + " — Host"
	tmpl, err := parseChrome("html/pages/body/room-host.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
		Room database.Room
		Game database.Game
	}
	_ = tmpl.ExecuteTemplate(w, "base", data{BasePageData: base, Room: room, Game: game})
}

// PlayPage is the phone controller for a seated player.
func PlayPage(w http.ResponseWriter, r *http.Request) {
	base := gsApi.GetBasePageData(r)
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	room, err := database.GetRoomByCode(code)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("No room with that code."))
		return
	}

	userId := base.User.Id
	if userId == uuid.Nil {
		http.Redirect(w, r, "/room/"+room.Code, http.StatusSeeOther)
		return
	}

	player, err := gsDatabase.GetLobbyUserPlayer(room.LobbyId, userId)
	if err != nil || player.Id == uuid.Nil {
		http.Redirect(w, r, "/room/"+room.Code, http.StatusSeeOther)
		return
	}

	game, err := database.GetGame(room.LobbyId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to load game."))
		return
	}

	base.PageTitle = "Room " + room.Code
	tmpl, err := parseChrome("html/pages/body/room-play.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	displayName := base.User.Name
	if i := strings.Index(displayName, "·"); i > 0 {
		displayName = displayName[:i]
	}

	type data struct {
		gsApi.BasePageData
		Room        database.Room
		Game        database.Game
		LobbyId     uuid.UUID
		DisplayName string
	}
	_ = tmpl.ExecuteTemplate(w, "base", data{
		BasePageData: base,
		Room:         room,
		Game:         game,
		LobbyId:      room.LobbyId,
		DisplayName:  displayName,
	})
}
