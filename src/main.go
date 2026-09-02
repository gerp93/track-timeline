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

	apiPages "github.com/gerp93/track-timeline/api/pages"
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

	// websocket (no middleware wrapper; reads {lobbyId} from the path itself)
	http.HandleFunc("GET /ws/lobby/{lobbyId}", gsWebsocket.ServeWs)

	gsBootstrap.Serve("TRACK_TIMELINE")
}
