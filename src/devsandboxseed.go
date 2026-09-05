//go:build devsandboxseed

// Command devsandboxseed seeds a local track_timeline_dev database with the
// admin/category defaults, three test players, a deck of cards (including a
// multi-byte "Björk"-style card for the mojibake regression), and a lobby
// with everyone already joined -- ready to open in a browser and click Start
// Game.
//
// Claude-sandbox tooling only -- see dev/claude-sandbox/README.md. It lives
// behind a build tag, in this module, rather than as a standalone program
// under dev/claude-sandbox/: a separate go.mod there can't resolve its
// dependency graph offline in this sandbox (no access to the Go module
// proxy, and the local module cache only holds exactly the versions this
// module's own go.sum already pinned, not the full transitive closure a
// fresh `go mod tidy` needs to re-derive). Building inside this module
// reuses the versions already resolved for the real server.
//
// The build tag keeps it out of every normal command -- `go build ./...`,
// `go vet ./...`, and `go test ./...` never see this file, so it can't
// break the real build or drag its dependencies into the shipped binary.
// Invoke it explicitly, as a single ad-hoc file (so it never collides with
// main.go's own func main):
//
//	go run -tags devsandboxseed ./devsandboxseed.go
//
// Requires dev/claude-sandbox/setup-mariadb.sh to have been run first, and
// TRACK_TIMELINE_SQL_* pointed at track_timeline_dev (see that script's own
// printed instructions). Safe to re-run: CreateUser on an existing name just
// logs and continues, and the deck/lobby/cards are always created fresh
// (each run's video IDs are suffixed by timestamp so re-running doesn't hit
// the DECK_ID+YOUTUBE_VIDEO_ID unique constraint).
package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	gsAuth "github.com/gerp93/gameshell-framework/auth"
	gsBootstrap "github.com/gerp93/gameshell-framework/bootstrap"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	gsStatic "github.com/gerp93/gameshell-framework/static"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/static"
)

const seedPlayerPassword = "PlaytestPass123!"

func main() {
	dbName := os.Getenv("TRACK_TIMELINE_SQL_DATABASE")
	if !strings.HasPrefix(dbName, "track_timeline_dev") {
		log.Fatalf("refusing to seed %q; set TRACK_TIMELINE_SQL_DATABASE=track_timeline_dev (see dev/claude-sandbox/setup-mariadb.sh)", dbName)
	}

	gsDatabase.SetEnvVarPrefix("TRACK_TIMELINE")
	gsAuth.SetCookiePrefix("TRACK-TIMELINE")
	if _, err := gsDatabase.CreateDatabaseConnection(); err != nil {
		log.Fatalf("db connect: %v", err)
	}

	// Mirrors main.go's own boot sequence so this also works against a
	// completely empty database, not just one the real server has already
	// applied schema to.
	features := gsBootstrap.Features{
		Decks: true, DecksListPageOverride: true, WinCelebration: true,
		LoseCelebration: true, WinVideo: true, LobbyTurnTimer: true,
	}
	gsBootstrap.ApplySchema(gsStatic.StaticFiles, gsStatic.SQLFiles)
	gsBootstrap.ApplyFeatureSchema(features)
	gsBootstrap.ApplySchema(static.StaticFiles, static.SQLFiles)
	if err := database.SeedAdminIfNoUsers(); err != nil {
		log.Fatalln(err)
	}
	if err := database.SeedDefaultCategoriesIfNone(); err != nil {
		log.Fatalln(err)
	}

	names := []string{"Alice", "Bob", "Carol"}
	var playerUserIds []uuid.UUID
	for _, n := range names {
		if err := gsDatabase.CreateUser(n, seedPlayerPassword, true); err != nil {
			log.Printf("create user %s (probably already exists, continuing): %v", n, err)
		}
		id, err := gsDatabase.GetUserIdByName(n)
		if err != nil {
			log.Fatalf("get user %s: %v", n, err)
		}
		playerUserIds = append(playerUserIds, id)
	}

	stamp := time.Now().Format("150405")
	deckId, err := gsDatabase.CreateDeck("Sandbox deck "+stamp, "", true)
	if err != nil {
		log.Fatalf("create deck: %v", err)
	}
	cards := []struct {
		title, artist, videoSuffix string
		year                       int
	}{
		{"Song One", "Artist One", "01", 1990},
		{"Song Two", "Artist Two", "02", 1995},
		{"Song Three", "Artist Three", "03", 2000},
		{"Song Four", "Artist Four", "04", 2005},
		{"Song Five", "Artist Five", "05", 2010},
		// Multi-byte-character regression card (see truncateRunes in
		// api/tracktimeline/round.go) -- guess "Hunter" by "Björk" in the
		// game to re-exercise the mojibake fix by hand.
		{"Hunter", "Björk", "06", 1997},
	}
	for _, c := range cards {
		year := sql.NullInt64{Int64: int64(c.year), Valid: true}
		videoId := "sbx" + stamp + c.videoSuffix
		if _, err := database.CreateCard(deckId, videoId, c.title, c.artist, year, uuid.NullUUID{}); err != nil {
			log.Fatalf("create card %q: %v", c.title, err)
		}
	}

	lobbyId, err := database.CreateLobby("Sandbox lobby "+stamp, "", "")
	if err != nil {
		log.Fatalf("create lobby: %v", err)
	}
	for _, id := range playerUserIds {
		if err := gsDatabase.AddUserLobbyAccess(id, lobbyId); err != nil {
			log.Fatalf("grant lobby access: %v", err)
		}
		if _, err := gsDatabase.AddUserToLobby(lobbyId, id); err != nil {
			log.Fatalf("join lobby: %v", err)
		}
	}

	// A lobby created outside the real /api/track-timeline/create flow has no
	// game or draw pile, and the lobby page redirects to /track-timeline/lobbies
	// the moment it notices that (see api/pages/pages.go's TrackTimelineLobby) --
	// so these two calls aren't optional bookkeeping, they're what makes the
	// lobby page actually load at all.
	gameId, err := database.CreateGame(lobbyId, 10, 6, database.GuessModeBoth,
		database.DefaultGuessMatchPercent, database.GuessJudgeLocal, database.PlaybackIntro, 20)
	if err != nil {
		log.Fatalf("create game: %v", err)
	}
	if err := database.InitializeDrawPile(gameId, []uuid.UUID{deckId}, nil); err != nil {
		log.Fatalf("init draw pile: %v", err)
	}

	fmt.Println("Seeded track_timeline_dev:")
	fmt.Printf("  players: %s (all password %q)\n", strings.Join(names, ", "), seedPlayerPassword)
	fmt.Printf("  deck: %d cards, incl. a Bjork multi-byte regression card\n", len(cards))
	fmt.Printf("  lobby: http://127.0.0.1:2016/track-timeline/%s\n", lobbyId)
	fmt.Println("  Log each player in first: POST /api/user/login (form: name, password) in that browser context/cookie jar, then open the lobby URL.")
}
