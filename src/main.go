package main

import (
	"log"
	"net/http"
	"time"

	gameshell "github.com/gerp93/gameshell-framework"
	gsApi "github.com/gerp93/gameshell-framework/api"
	gsApiUser "github.com/gerp93/gameshell-framework/api/user"
	gsAuth "github.com/gerp93/gameshell-framework/auth"
	gsBootstrap "github.com/gerp93/gameshell-framework/bootstrap"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	gsStatic "github.com/gerp93/gameshell-framework/static"
	gsWebsocket "github.com/gerp93/gameshell-framework/websocket"

	apiAccess "github.com/gerp93/track-timeline/api/access"
	apiCard "github.com/gerp93/track-timeline/api/card"
	apiCategory "github.com/gerp93/track-timeline/api/category"
	apiPages "github.com/gerp93/track-timeline/api/pages"
	apiTrackTimeline "github.com/gerp93/track-timeline/api/tracktimeline"
	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/game"
	"github.com/gerp93/track-timeline/static"
)

func main() {
	defer func() {
		if err := recover(); err != nil {
			log.Println("panic occurred:", err)
		}
	}()

	gameshell.Register(game.TrackTimeline{})
	gsApi.SetBrandName("Track Timeline")
	gsAuth.SetCookiePrefix("TRACK-TIMELINE")
	gsApi.SetPagePolicy(gsApi.PagePolicy{
		LoginPaths: []string{"/account", "/users", "/categories", "/stats"},
		LoginPathPrefixes: []string{
			"/deck",
			"/track-timeline",
			"/stats",
		},
		AdminPaths: []string{"/users", "/categories"},
	})
	gsDatabase.SetEnvVarPrefix("TRACK_TIMELINE")
	gsApiUser.SetMaxWinGifBytes(1000 * 1024)
	features := gsBootstrap.Features{
		Decks:           true,
		WinCelebration:  true,
		LoseCelebration: true,
		LobbyTurnTimer:  true,
	}
	gsBootstrap.MountFeatures(features)

	db := gsBootstrap.ConnectWithRetry(6, 10*time.Second)
	defer db.Close()

	// framework schema first, game schema depends on it (CARD FKs to DECK)
	gsBootstrap.ApplySchema(gsStatic.StaticFiles, gsStatic.SQLFiles)
	gsBootstrap.ApplyFeatureSchema(features)
	gsBootstrap.ApplySchema(static.StaticFiles, static.SQLFiles)

	if err := database.SeedAdminIfNoUsers(); err != nil {
		log.Fatalln(err)
		return
	}

	// static files (game's own at /static/, shared framework assets at /gs/)
	gsBootstrap.MountStaticAssets(static.StaticFiles)

	// pages (game-owned; the framework's core and Features-gated pages are
	// wired by MountFeatures above)
	//
	// "/{$}" (not "/"): a bare "/" is a Go 1.22+ subtree wildcard matching every
	// unmatched path, silently serving Home for any bad URL instead of a real
	// 404. "{$}" restricts the match to the literal root only.
	http.Handle("GET /{$}", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.Home)))
	http.Handle("GET /about", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.About)))
	http.Handle("GET /categories", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.Categories)))
	http.Handle("GET /deck/{deckId}", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.Deck)))

	// card (game-owned: a card is a song, so its shape is not the framework's
	// business)
	http.Handle("GET /api/deck/{deckId}/card-export", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.GetCardExport)))
	http.Handle("POST /api/deck/{deckId}/card-import", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.ImportJSON)))
	http.Handle("POST /api/card/create", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.Create)))
	http.Handle("PUT /api/card/{cardId}", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.Update)))
	http.Handle("DELETE /api/card/{cardId}", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.Delete)))

	// genre (admin-managed; MiddlewareForAPIs only enforces login, so the
	// admin check lives in the handlers). Delete-with-reassign is a POST rather
	// than a DELETE because it carries a form body naming the destination, and
	// Go's ParseForm only reads the body for POST/PUT/PATCH.
	http.Handle("POST /api/category/create", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCategory.Create)))
	http.Handle("POST /api/category/{categoryId}/delete", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCategory.DeleteReassign)))

	// track-timeline pages
	http.Handle("GET /track-timeline/lobbies", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.TrackTimelineLobbies)))
	http.Handle("GET /track-timeline/{lobbyId}", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.TrackTimelineLobby)))
	http.Handle("GET /track-timeline/{lobbyId}/access", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.TrackTimelineLobbyAccess)))

	// lobby setup
	http.Handle("POST /api/track-timeline/create", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.Create)))
	http.Handle("POST /api/track-timeline/search", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.Search)))
	http.Handle("POST /api/track-timeline/card-count", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.CardCount)))

	// gameplay
	http.Handle("POST /api/track-timeline/{lobbyId}/start", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.StartGame)))
	http.Handle("POST /api/track-timeline/{lobbyId}/reset", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.ResetGame)))
	http.Handle("POST /api/track-timeline/{lobbyId}/play-song", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.PlaySong)))
	http.Handle("POST /api/track-timeline/{lobbyId}/place-card", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.PlaceCard)))
	http.Handle("POST /api/track-timeline/{lobbyId}/challenge", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.Challenge)))
	http.Handle("POST /api/track-timeline/{lobbyId}/close-challenge", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.CloseChallenge)))
	http.Handle("POST /api/track-timeline/{lobbyId}/guess", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.SubmitGuess)))
	http.Handle("POST /api/track-timeline/{lobbyId}/skip-card", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.SkipCard)))
	http.Handle("POST /api/track-timeline/{lobbyId}/timeout", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.TimeoutPass)))
	http.Handle("PUT /api/track-timeline/{lobbyId}/message", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.SetLobbyMessage)))

	// gameplay fragments
	http.Handle("GET /api/track-timeline/{lobbyId}/current-card", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.GetCurrentCard)))
	http.Handle("GET /api/track-timeline/{lobbyId}/timeline", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.GetTimeline)))
	http.Handle("GET /api/track-timeline/{lobbyId}/players", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.GetPlayers)))
	http.Handle("GET /api/track-timeline/{lobbyId}/draw-pile-count", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.GetDrawPileCount)))
	http.Handle("GET /api/track-timeline/{lobbyId}/decks", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.GetDecks)))

	// access gates
	http.Handle("POST /api/access/lobby/{lobbyId}", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiAccess.Lobby)))
	http.Handle("POST /api/access/deck/{deckId}", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiAccess.Deck)))

	// websocket (no middleware wrapper; reads {lobbyId} from the path itself)
	http.HandleFunc("GET /ws/lobby/{lobbyId}", gsWebsocket.ServeWs)

	gsBootstrap.Serve("TRACK_TIMELINE")
}
