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
	apiRoom "github.com/gerp93/track-timeline/api/room"
	apiTrackTimeline "github.com/gerp93/track-timeline/api/tracktimeline"
	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/game"
	"github.com/gerp93/track-timeline/static"
	"github.com/gerp93/track-timeline/videocheck"
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
		LoginPaths: []string{"/account", "/users", "/categories", "/stats", "/videos", "/guess-test"},
		LoginPathPrefixes: []string{
			"/deck",
			"/track-timeline",
			"/room/create",
			"/stats",
		},
		AdminPaths: []string{"/users", "/categories", "/videos", "/guess-test"},
	})
	gsDatabase.SetEnvVarPrefix("TRACK_TIMELINE")
	gsApiUser.SetMaxWinGifBytes(1000 * 1024)
	features := gsBootstrap.Features{
		Decks:                 true,
		DecksListPageOverride: true,
		WinCelebration:        true,
		LoseCelebration:       true,
		WinVideo:              true,
		LobbyTurnTimer:        true,
	}
	gsBootstrap.MountFeatures(features)

	db := gsBootstrap.ConnectWithRetry(6, 10*time.Second)
	defer db.Close()

	// framework schema first, game schema depends on it (CARD FKs to DECK)
	gsBootstrap.ApplySchema(gsStatic.StaticFiles, gsStatic.SQLFiles)
	gsBootstrap.ApplyFeatureSchema(features)
	gsBootstrap.ApplySchema(static.StaticFiles, static.SQLFiles)

	videocheck.SetQuotaRecorder(func(units int) {
		if err := database.AddYouTubeQuotaUsage(units); err != nil {
			log.Println(err)
		}
	})

	if err := database.SeedAdminIfNoUsers(); err != nil {
		log.Fatalln(err)
		return
	}

	if err := database.SeedDefaultCategoriesIfNone(); err != nil {
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
	http.Handle("GET /videos", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.DeadVideos)))
	http.Handle("GET /guess-test", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.GuessTest)))
	http.Handle("GET /deck/{deckId}", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.Deck)))

	// Overrides the framework's own /decks (see Features.DecksListPageOverride
	// above) so the admin Library button can be injected into the shared
	// decks.html chrome via deck-list-video-health.html.
	http.Handle("GET /decks", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.Decks)))

	// card (game-owned: a card is a song, so its shape is not the framework's
	// business)
	http.Handle("GET /api/deck/{deckId}/card-export", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.GetCardExport)))
	http.Handle("POST /api/deck/{deckId}/card-import", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.ImportJSON)))
	http.Handle("POST /api/guess-test", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.TestGuess)))
	http.Handle("POST /api/videos/check-stale", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.CheckStaleVideos)))
	http.Handle("POST /api/videos/auto-check-stale", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.AutoCheckStaleVideos)))
	http.Handle("GET /api/videos/library-issues-button", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.LibraryIssuesButton)))
	http.Handle("POST /api/videos/resolve", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.ResolveDeadVideos)))
	http.Handle("POST /api/videos/find-videos", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.FindVideos)))
	http.Handle("POST /api/videos/mark-ok", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.MarkVideosOk)))
	http.Handle("POST /api/videos/mark-unavailable", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.MarkVideosUnavailable)))
	http.Handle("POST /api/videos/mark-incorrect", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.MarkVideosIncorrect)))
	http.Handle("POST /api/videos/delete", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.DeleteCards)))
	http.Handle("GET /api/videos/dead-export", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.ExportDeadVideos)))
	http.Handle("POST /api/videos/import-links", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.ImportVideoLinks)))
	http.Handle("POST /api/videos/dismiss-duplicates", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.DismissDuplicateGroup)))
	http.Handle("POST /api/videos/undismiss-duplicates", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.UndismissDuplicateGroup)))
	http.Handle("POST /api/videos/delete-exact-duplicate-latests", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.DeleteExactDuplicateLatests)))
	http.Handle("POST /api/videos/delete-exact-title-artist-latests", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.DeleteExactTitleArtistLatests)))
	http.Handle("POST /api/card/create", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.Create)))
	http.Handle("PUT /api/card/{cardId}", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.Update)))
	http.Handle("PUT /api/card/{cardId}/category", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.SetCategory)))
	http.Handle("POST /api/card/{cardId}/suggest-genre", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.SuggestGenre)))
	http.Handle("POST /api/videos/suggest-genres", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.SuggestGenres)))
	http.Handle("PUT /api/card/{cardId}/video", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.UpdateVideo)))
	http.Handle("POST /api/card/{cardId}/find-video", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.FindVideo)))
	http.Handle("POST /api/card/{cardId}/mark-ok", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.MarkVideoOk)))
	http.Handle("POST /api/card/{cardId}/mark-unavailable", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.MarkVideoUnavailable)))
	http.Handle("POST /api/card/{cardId}/mark-incorrect", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiCard.MarkVideoIncorrect)))
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

	// room mode (seatless host TV + phone seats). Join/play are public so
	// guests can sit without an account; create requires login via policy.
	http.Handle("GET /room/create", gsApi.MiddlewareForPages(http.HandlerFunc(apiRoom.CreatePage)))
	http.Handle("GET /room/{code}", gsApi.MiddlewareForPages(http.HandlerFunc(apiRoom.JoinPage)))
	http.Handle("GET /room/{code}/host", gsApi.MiddlewareForPages(http.HandlerFunc(apiRoom.HostPage)))
	http.Handle("GET /room/{code}/play", gsApi.MiddlewareForPages(http.HandlerFunc(apiRoom.PlayPage)))
	http.Handle("POST /api/room/create", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiRoom.Create)))
	http.Handle("POST /api/room/{code}/join-guest", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiRoom.JoinGuest)))
	http.Handle("POST /api/room/{code}/join-account", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiRoom.JoinAccount)))
	http.Handle("GET /api/room/{code}/host/current-card", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiRoom.HostCurrentCard)))
	http.Handle("GET /api/room/{code}/host/timeline", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiRoom.HostTimeline)))
	http.HandleFunc("GET /ws/room/{code}", apiRoom.ServeWs)

	// stats pages
	http.Handle("GET /stats", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.Stats)))
	http.Handle("GET /stats/leaderboard", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.StatsLeaderboard)))
	http.Handle("GET /stats/users", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.StatsUsers)))
	http.Handle("GET /stats/user/{userId}", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.StatsUser)))
	http.Handle("GET /stats/cards", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.StatsCards)))
	http.Handle("GET /stats/card/{cardId}", gsApi.MiddlewareForPages(http.HandlerFunc(apiPages.StatsCard)))

	// lobby setup
	http.Handle("POST /api/track-timeline/create", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.Create)))
	http.Handle("POST /api/track-timeline/search", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.Search)))
	http.Handle("POST /api/track-timeline/card-count", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.CardCount)))

	// gameplay
	http.Handle("POST /api/track-timeline/{lobbyId}/start", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.StartGame)))
	http.Handle("POST /api/track-timeline/{lobbyId}/reset", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.ResetGame)))
	http.Handle("POST /api/track-timeline/{lobbyId}/play-song", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.PlaySong)))
	http.Handle("POST /api/track-timeline/{lobbyId}/pause-song", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.PauseSong)))
	http.Handle("POST /api/track-timeline/{lobbyId}/resume-song", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.ResumeSong)))
	http.Handle("POST /api/track-timeline/{lobbyId}/replay-song", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.ReplaySong)))
	http.Handle("POST /api/track-timeline/{lobbyId}/place-card", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.PlaceCard)))
	http.Handle("POST /api/track-timeline/{lobbyId}/buy-card", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.BuyCard)))
	http.Handle("POST /api/track-timeline/{lobbyId}/claim-steal", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.ClaimSteal)))
	http.Handle("POST /api/track-timeline/{lobbyId}/attempt-steal", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.AttemptSteal)))
	http.Handle("POST /api/track-timeline/{lobbyId}/guess", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.SubmitGuess)))
	http.Handle("POST /api/track-timeline/{lobbyId}/skip-card", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.SkipCard)))
	http.Handle("POST /api/track-timeline/{lobbyId}/dead-video", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.ReportDeadVideo)))
	http.Handle("POST /api/track-timeline/{lobbyId}/timeout", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.TimeoutPass)))
	http.Handle("PUT /api/track-timeline/{lobbyId}/message", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.SetLobbyMessage)))

	// gameplay fragments
	http.Handle("GET /api/track-timeline/{lobbyId}/current-card", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.GetCurrentCard)))
	http.Handle("GET /api/track-timeline/{lobbyId}/timeline", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.GetTimeline)))
	http.Handle("GET /api/track-timeline/{lobbyId}/draw-pile-count", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.GetDrawPileCount)))
	http.Handle("GET /api/track-timeline/{lobbyId}/decks", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiTrackTimeline.GetDecks)))

	// access gates
	http.Handle("POST /api/access/lobby/{lobbyId}", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiAccess.Lobby)))
	http.Handle("POST /api/access/deck/{deckId}", gsApi.MiddlewareForAPIs(http.HandlerFunc(apiAccess.Deck)))

	// websocket (no middleware wrapper; reads {lobbyId} from the path itself)
	http.HandleFunc("GET /ws/lobby/{lobbyId}", gsWebsocket.ServeWs)

	gsBootstrap.Serve("TRACK_TIMELINE")
}
