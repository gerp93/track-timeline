package apiPages

import (
	"encoding/json"
	"html/template"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	gsApi "github.com/gerp93/gameshell-framework/api"
	gsApiPages "github.com/gerp93/gameshell-framework/api/pages"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	gsStatic "github.com/gerp93/gameshell-framework/static"
	"github.com/google/uuid"

	apiCard "github.com/gerp93/track-timeline/api/card"
	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/guess"
	"github.com/gerp93/track-timeline/static"
)

// parseChrome builds a template set from the framework's base.html plus one of
// this repo's page bodies. It is two ParseFS calls because the two files live
// in different embedded filesystems: base.html ships with the framework, the
// body with the game.
//
// Exactly one body file may be parsed per request. Every page body in this repo
// and in the framework defines {{define "body"}}, and text/template silently
// lets a second definition overwrite the first with no compile-time signal — so
// a composed parse must use distinctly-named blocks instead (see Deck).
func parseChrome(bodyPattern string, funcMap template.FuncMap) (*template.Template, error) {
	t := template.New("base.html")
	if funcMap != nil {
		t = t.Funcs(funcMap)
	}
	t, err := t.ParseFS(gsStatic.StaticFiles, "html/pages/base.html")
	if err != nil {
		return nil, err
	}
	return t.ParseFS(static.StaticFiles, bodyPattern)
}

func Home(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)
	basePageData.PageTitle = "Track Timeline"

	libraryIssueCount, ok := libraryIssueCountFor(w, basePageData.User.IsAdmin)
	if !ok {
		return
	}

	tmpl, err := parseChrome("html/pages/body/home.html", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
		LibraryIssueCount int
	}

	_ = tmpl.ExecuteTemplate(w, "base", data{
		BasePageData:      basePageData,
		LibraryIssueCount: libraryIssueCount,
	})
}

func About(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)
	basePageData.PageTitle = "About"

	tmpl, err := parseChrome("html/pages/body/about.html", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
	}

	_ = tmpl.ExecuteTemplate(w, "base", data{BasePageData: basePageData})
}

// Categories is the admin page for the genre list.
func Categories(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)
	basePageData.PageTitle = "Genres"

	categories, err := database.GetCategories()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get genres."))
		return
	}

	// Card counts drive the "this will move N cards" warning on delete.
	type categoryRow struct {
		database.Category
		CardCount int
	}
	rows := make([]categoryRow, 0, len(categories))
	for _, category := range categories {
		count, err := database.CountCardsInCategory(category.Id)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to count cards in genre."))
			return
		}
		rows = append(rows, categoryRow{Category: category, CardCount: count})
	}

	tmpl, err := parseChrome("html/pages/body/categories.html", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
		Categories []categoryRow
		All        []database.Category
	}

	_ = tmpl.ExecuteTemplate(w, "base", data{
		BasePageData: basePageData,
		Categories:   rows,
		All:          categories,
	})
}

// Deck is the deck detail page: the framework owns the chrome (header, export,
// edit dialog, danger zone), this repo owns the card table and its dialogs,
// because the card shape is game-specific.
//
// The fragments below define card-header-actions / card-management /
// card-search-controls, never "body" — the chrome already defines that, and a
// second definition would silently overwrite it.
func Deck(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)

	deckId, err := uuid.Parse(r.PathValue("deckId"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid deck."))
		return
	}

	// Page handlers must take the user from BasePageData, never from
	// gsApi.GetUserId: MiddlewareForPages populates only the base-page-data
	// context key, and GetUserId type-asserts a key that only
	// MiddlewareForAPIs sets — calling it here panics on a nil interface.
	userId := basePageData.User.Id
	hasAccess, err := gsDatabase.UserHasDeckAccess(userId, deckId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check deck access."))
		return
	}
	if !hasAccess {
		http.Redirect(w, r, "/deck/"+deckId.String()+"/access", http.StatusSeeOther)
		return
	}

	deck, err := gsDatabase.GetDeck(deckId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get deck."))
		return
	}
	if deck.Id == uuid.Nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("No deck found."))
		return
	}
	basePageData.PageTitle = deck.Name

	var search string
	var videoFilter string
	page := 1
	for key, val := range r.URL.Query() {
		switch key {
		case "search":
			search = val[0]
		case "video":
			videoFilter = val[0]
		case "page":
			if parsed, err := strconv.Atoi(val[0]); err == nil {
				page = parsed
			}
		}
	}
	if page < 1 {
		page = 1
	}
	switch videoFilter {
	case "available", "unavailable":
	default:
		videoFilter = ""
	}

	rowCount, err := database.CountCardsInDeck(deckId, search, videoFilter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to count cards."))
		return
	}
	lastPage := int(math.Ceil(float64(rowCount) / 10))
	if lastPage < 1 {
		lastPage = 1
	}
	if page > lastPage {
		page = lastPage
	}

	cards, err := database.SearchCardsInDeck(deckId, search, page, videoFilter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get cards."))
		return
	}

	categories, err := database.GetCategories()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get genres."))
		return
	}

	unavailableCount, err := database.CountUnavailableVideosInDeck(deckId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to count unavailable videos."))
		return
	}

	tmpl, err := gsApiPages.ParseGameFragment(
		static.StaticFiles,
		"html/pages/body/deck-detail-chrome.html",
		"html/pages/body/deck-card-management.html",
		"html/pages/body/deck-search-controls.html",
	)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
		Deck             gsDatabase.Deck
		Cards            []database.Card
		Categories       []database.Category
		Search           string
		VideoFilter      string
		Page             int
		LastPage         int
		RowCount         int
		UnavailableCount int
	}

	_ = tmpl.ExecuteTemplate(w, "base", data{
		BasePageData:     basePageData,
		Deck:             deck,
		Cards:            cards,
		Categories:       categories,
		Search:           search,
		VideoFilter:      videoFilter,
		Page:             page,
		LastPage:         lastPage,
		RowCount:         rowCount,
		UnavailableCount: unavailableCount,
	})
}

// Decks overrides the framework's own /decks page (see main.go's
// Features.DecksListPageOverride) so the admin Library button can be
// injected into the shared decks.html chrome via
// deck-list-video-health.html's deck-list-extra-filter block. Deck search,
// paging and creation stay entirely the framework's own
// (gsDatabase.SearchDecks/CountDecks).
func Decks(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)
	basePageData.PageTitle = basePageData.BrandName + " - Decks"

	var name string
	var page int
	params := r.URL.Query()
	for key, val := range params {
		switch key {
		case "name":
			name = val[0]
		case "page":
			page, _ = strconv.Atoi(val[0])
		}
	}

	totalRowCount, err := gsDatabase.CountDecks(name)
	if err != nil {
		totalRowCount = 0
	}
	totalPageCount := max((totalRowCount+9)/10, 1)

	if page < 1 {
		page = 1
	}
	if page > totalPageCount {
		page = totalPageCount
	}

	decks, err := gsDatabase.SearchDecks(name, page)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("failed to get table rows"))
		return
	}

	libraryIssueCount, ok := libraryIssueCountFor(w, basePageData.User.IsAdmin)
	if !ok {
		return
	}

	tmpl, err := gsApiPages.ParseGameFragment(
		static.StaticFiles,
		"html/pages/body/decks.html",
		"html/pages/body/deck-list-video-health.html",
	)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("failed to parse HTML"))
		return
	}

	type data struct {
		gsApi.BasePageData
		Name              string
		Page              int
		LastPage          int
		RowCount          int
		Decks             []gsDatabase.DeckDetails
		LibraryIssueCount int
	}

	_ = tmpl.ExecuteTemplate(w, "base", data{
		BasePageData:      basePageData,
		Name:              name,
		Page:              page,
		LastPage:          totalPageCount,
		RowCount:          totalRowCount,
		Decks:             decks,
		LibraryIssueCount: libraryIssueCount,
	})
}

// libraryIssueCountFor loads the admin Library badge. Non-admins skip the
// queries. On failure it writes the response and returns false.
func libraryIssueCountFor(w http.ResponseWriter, isAdmin bool) (int, bool) {
	if !isAdmin {
		return 0, true
	}
	count, err := database.CountLibraryIssues()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to count library issues."))
		return 0, false
	}
	return count, true
}

// TrackTimelineLobbies is the lobby list and the new-game form.
func TrackTimelineLobbies(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)
	basePageData.PageTitle = "Lobbies"

	decks, err := gsDatabase.GetReadableDecks(basePageData.User.Id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get decks."))
		return
	}

	categories, err := database.GetCategories()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get genres."))
		return
	}

	tmpl, err := parseChrome("html/pages/body/track-timeline-lobbies.html", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
		Decks       []gsDatabase.Deck
		Categories  []database.Category
		ClaudeReady bool
	}

	_ = tmpl.ExecuteTemplate(w, "base", data{
		BasePageData: basePageData,
		Decks:        decks,
		Categories:   categories,
		ClaudeReady:  guess.ClaudeConfigured(),
	})
}

// TrackTimelineLobby is the game board. Visiting is what joins you to the
// lobby, so a player who follows a link is seated without a separate step.
func TrackTimelineLobby(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)

	lobbyId, err := uuid.Parse(r.PathValue("lobbyId"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid lobby."))
		return
	}

	userId := basePageData.User.Id
	hasAccess, err := gsDatabase.UserHasLobbyAccess(userId, lobbyId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to check lobby access."))
		return
	}
	if !hasAccess {
		http.Redirect(w, r, "/track-timeline/"+lobbyId.String()+"/access", http.StatusSeeOther)
		return
	}

	lobby, err := database.GetLobby(lobbyId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get lobby."))
		return
	}
	if lobby.Id == uuid.Nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("No lobby found."))
		return
	}
	basePageData.PageTitle = lobby.Name

	if _, err := gsDatabase.AddUserToLobby(lobbyId, userId); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to join the lobby."))
		return
	}

	game, err := database.GetGame(lobbyId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get game."))
		return
	}
	if game.Id == uuid.Nil {
		// A lobby always gets its game and draw pile at creation time, so this
		// is a lobby made outside that flow and there is nothing to play.
		http.Redirect(w, r, "/track-timeline/lobbies", http.StatusSeeOther)
		return
	}

	drawPileCount, err := database.GetDrawPileCount(game.Id)
	if err != nil {
		drawPileCount = 0
	}

	yearRanges, err := database.GetYearRanges(game.Id)
	if err != nil {
		yearRanges = nil
	}

	turnTimerSeconds, err := gsDatabase.GetLobbyTurnTimerSeconds(lobbyId)
	if err != nil {
		turnTimerSeconds = 0
	}

	winnerName := ""
	if game.WinnerId.Valid {
		if user, err := gsDatabase.GetUser(game.WinnerId.UUID); err == nil {
			winnerName = user.Name
		}
	}

	tmpl, err := parseChrome("html/pages/body/track-timeline.html", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
		Lobby            database.Lobby
		Game             database.Game
		DrawPileCount    int
		YearRanges       []database.YearRange
		TurnTimerSeconds int
		WinnerName       string
	}

	_ = tmpl.ExecuteTemplate(w, "base", data{
		BasePageData:     basePageData,
		Lobby:            lobby,
		Game:             game,
		DrawPileCount:    drawPileCount,
		YearRanges:       yearRanges,
		TurnTimerSeconds: turnTimerSeconds,
		WinnerName:       winnerName,
	})
}

// TrackTimelineLobbyAccess is the password gate for a protected lobby.
func TrackTimelineLobbyAccess(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)

	lobbyId, err := uuid.Parse(r.PathValue("lobbyId"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid lobby."))
		return
	}

	lobby, err := database.GetLobby(lobbyId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get lobby."))
		return
	}
	if lobby.Id == uuid.Nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("No lobby found."))
		return
	}
	basePageData.PageTitle = lobby.Name

	tmpl, err := parseChrome("html/pages/body/lobby-access.html", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
		Lobby database.Lobby
	}

	_ = tmpl.ExecuteTemplate(w, "base", data{BasePageData: basePageData, Lobby: lobby})
}

// Stats is the statistics hub: links into leaderboard, users, and cards.
func Stats(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)
	basePageData.PageTitle = "Stats"

	tmpl, err := parseChrome("html/pages/body/stats.html", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
	}

	_ = tmpl.ExecuteTemplate(w, "base", data{BasePageData: basePageData})
}

// StatsLeaderboard ranks users by wins.
func StatsLeaderboard(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)
	basePageData.PageTitle = "Leaderboard"

	rows, err := database.GetLeaderboard()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get the leaderboard."))
		return
	}

	tmpl, err := parseChrome("html/pages/body/stats-leaderboard.html", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
		Rows []database.LeaderboardRow
	}

	_ = tmpl.ExecuteTemplate(w, "base", data{BasePageData: basePageData, Rows: rows})
}

// StatsUsers lists every user with at least one recorded placement or guess.
func StatsUsers(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)
	basePageData.PageTitle = "Players"

	rows, err := database.GetUserStatsList()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get player stats."))
		return
	}

	tmpl, err := parseChrome("html/pages/body/stats-users.html", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
		Rows []database.UserStatsRow
	}

	_ = tmpl.ExecuteTemplate(w, "base", data{BasePageData: basePageData, Rows: rows})
}

// StatsUser is one player's detail page.
func StatsUser(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)

	userId, err := uuid.Parse(r.PathValue("userId"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid player."))
		return
	}

	stats, err := database.GetUserStats(userId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get player stats."))
		return
	}
	basePageData.PageTitle = stats.UserName

	tmpl, err := parseChrome("html/pages/body/stats-user.html", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
		Stats database.UserStatsRow
	}

	_ = tmpl.ExecuteTemplate(w, "base", data{BasePageData: basePageData, Stats: stats})
}

// StatsCards lists the songs most often placed wrong.
func StatsCards(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)
	basePageData.PageTitle = "Hardest Songs"

	rows, err := database.GetHardestCards(3)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get song stats."))
		return
	}

	tmpl, err := parseChrome("html/pages/body/stats-cards.html", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
		Rows []database.CardStatsRow
	}

	_ = tmpl.ExecuteTemplate(w, "base", data{BasePageData: basePageData, Rows: rows})
}

// StatsCard is one song's detail page.
func StatsCard(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)

	cardId, err := uuid.Parse(r.PathValue("cardId"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid song."))
		return
	}

	stats, err := database.GetCardStats(cardId)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get song stats."))
		return
	}
	basePageData.PageTitle = stats.Title

	tmpl, err := parseChrome("html/pages/body/stats-card.html", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
		Stats database.CardStatsRow
	}

	_ = tmpl.ExecuteTemplate(w, "base", data{BasePageData: basePageData, Stats: stats})
}

// DeadVideos is the admin library page: dead/incorrect YouTube links and
// cross-deck title+artist duplicate clusters.
func DeadVideos(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)
	if !basePageData.User.IsAdmin {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can maintain the song library."))
		return
	}
	basePageData.PageTitle = basePageData.BrandName + " - Library"

	tab := r.URL.Query().Get("tab")
	switch tab {
	case "duplicates", "genres":
	default:
		tab = "dead"
	}

	search := r.URL.Query().Get("search")
	status := r.URL.Query().Get("status")
	switch status {
	case "unavailable", "awaiting", "incorrect":
	default:
		status = "all"
	}
	dupView := r.URL.Query().Get("dup")
	if dupView != "dismissed" {
		dupView = "active"
	}
	genreView := r.URL.Query().Get("genre")
	if genreView != "log" {
		genreView = "needs"
	}
	page := 1
	if raw := r.URL.Query().Get("page"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			page = parsed
		}
	}
	if page < 1 {
		page = 1
	}

	type data struct {
		gsApi.BasePageData
		Tab                         string
		DupView                     string
		GenreView                   string
		Cards                       []database.DeadVideoCard
		UngenredCards               []database.UngenredCard
		GenreAssignLogs             []database.GenreAssignLog
		DuplicateGroups             []database.DuplicateGroup
		Categories                  []database.Category
		Search                      string
		Status                      string
		ShowEmbeds                  bool
		Page                        int
		LastPage                    int
		RowCount                    int
		DeadTabCount                int
		DuplicateTabCount           int
		DismissedDupTabCount        int
		UngenredTabCount            int
		GenreLogTabCount            int
		ClaudeGenreReady            bool
		ExactDuplicateDeleteCount   int
		ExactTitleArtistDeleteCount int
		StaleCount                  int
		ResolveCount                int
		QuotaUsed                   int
		QuotaLimit                  int
		QuotaRemaining              int
	}

	out := data{
		BasePageData:     basePageData,
		Tab:              tab,
		DupView:          dupView,
		GenreView:        genreView,
		Search:           search,
		Status:           status,
		ShowEmbeds:       false,
		Page:             page,
		ClaudeGenreReady: guess.ClaudeConfigured(),
	}

	deadTabCount, err := database.CountDeadOrAwaitingVideos("", "all")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to count dead videos."))
		return
	}
	out.DeadTabCount = deadTabCount

	duplicateTabCount, err := database.CountDuplicateGroups()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to count duplicate groups."))
		return
	}
	out.DuplicateTabCount = duplicateTabCount

	dismissedDupTabCount, err := database.CountDismissedDuplicateGroups()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to count dismissed duplicates."))
		return
	}
	out.DismissedDupTabCount = dismissedDupTabCount

	ungenredTabCount, err := database.CountUngenredCards()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to count ungenred songs."))
		return
	}
	out.UngenredTabCount = ungenredTabCount

	genreLogTabCount, err := database.CountGenreAssignLogs()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to count Claude genre log rows."))
		return
	}
	out.GenreLogTabCount = genreLogTabCount

	switch tab {
	case "duplicates":
		candidates, err := database.ListDuplicateCandidates()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to list songs for duplicate check."))
			return
		}
		dismissed, err := database.ListDismissedDuplicatePairs()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to load dismissed duplicates."))
			return
		}
		var groups []database.DuplicateGroup
		if dupView == "dismissed" {
			groups = database.BuildDismissedDuplicateGroups(candidates, dismissed, search)
		} else {
			groups = database.BuildDuplicateGroups(candidates, dismissed, search)
			out.ExactDuplicateDeleteCount = len(database.ExactDuplicateLatestIds(candidates))
			out.ExactTitleArtistDeleteCount = len(database.ExactTitleArtistLatestIds(candidates))
		}
		out.RowCount = len(groups)
		lastPage := int(math.Ceil(float64(out.RowCount) / 10))
		if lastPage < 1 {
			lastPage = 1
		}
		if page > lastPage {
			page = lastPage
		}
		out.Page = page
		out.LastPage = lastPage
		start := (page - 1) * 10
		end := start + 10
		if start > len(groups) {
			start = len(groups)
		}
		if end > len(groups) {
			end = len(groups)
		}
		out.DuplicateGroups = groups[start:end]
	case "genres":
		categories, err := database.GetCategories()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to get genres."))
			return
		}
		out.Categories = categories

		if genreView == "log" {
			rowCount, err := database.CountGenreAssignLogsMatching(search)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("Failed to count Claude genre log rows."))
				return
			}
			lastPage := int(math.Ceil(float64(rowCount) / 10))
			if lastPage < 1 {
				lastPage = 1
			}
			if page > lastPage {
				page = lastPage
			}
			logs, err := database.SearchGenreAssignLogs(search, page)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("Failed to list Claude genre log."))
				return
			}
			out.GenreAssignLogs = logs
			out.Page = page
			out.LastPage = lastPage
			out.RowCount = rowCount
		} else {
			rowCount, err := database.CountUngenredCardsMatching(search)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("Failed to count ungenred songs."))
				return
			}
			lastPage := int(math.Ceil(float64(rowCount) / 10))
			if lastPage < 1 {
				lastPage = 1
			}
			if page > lastPage {
				page = lastPage
			}
			cards, err := database.SearchUngenredCards(search, page)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("Failed to list ungenred songs."))
				return
			}
			out.UngenredCards = cards
			out.Page = page
			out.LastPage = lastPage
			out.RowCount = rowCount
		}
	default:
		rowCount, err := database.CountDeadOrAwaitingVideos(search, status)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to count dead videos."))
			return
		}
		lastPage := int(math.Ceil(float64(rowCount) / 10))
		if lastPage < 1 {
			lastPage = 1
		}
		if page > lastPage {
			page = lastPage
		}

		cards, err := database.SearchDeadOrAwaitingVideos(search, page, status)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to list dead videos."))
			return
		}

		staleCount, err := database.CountStaleVideoChecks(apiCard.StaleVideoCutoff())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to count stale video checks."))
			return
		}

		resolveCount, err := database.CountDeadOrAwaitingVideos("", "awaiting")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to count songs awaiting recheck."))
			return
		}

		quotaUsed, err := database.GetYouTubeQuotaUsedToday()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to load YouTube quota usage."))
			return
		}
		quotaLimit := database.YouTubeDailyQuotaLimit()

		out.Cards = cards
		out.Page = page
		out.LastPage = lastPage
		out.RowCount = rowCount
		out.StaleCount = staleCount
		out.ResolveCount = resolveCount
		out.QuotaUsed = quotaUsed
		out.QuotaLimit = quotaLimit
		out.QuotaRemaining = max(0, quotaLimit-quotaUsed)
	}

	tmpl, err := parseChrome("html/pages/body/dead-videos.html", template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"formatTime": func(t time.Time) string {
			return t.Local().Format("2006-01-02 15:04:05")
		},
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	_ = tmpl.ExecuteTemplate(w, "base", out)
}

// GuessTest is Quizmaster Testing: an admin sandbox for the title/artist
// match engine. Pick a random song, type a guess, and see how the same rules
// as a lobby would score it. Nothing is written to a game.
func GuessTest(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)
	if !basePageData.User.IsAdmin {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can test the match engine."))
		return
	}
	basePageData.PageTitle = basePageData.BrandName + " - Quizmaster Testing"

	search := strings.TrimSpace(r.URL.Query().Get("q"))
	cardIdRaw := strings.TrimSpace(r.URL.Query().Get("cardId"))

	var card database.Card
	var results []database.Card
	var err error

	switch {
	case cardIdRaw != "":
		cardId, parseErr := uuid.Parse(cardIdRaw)
		if parseErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("That song id is not valid."))
			return
		}
		card, err = database.GetCard(cardId)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to load that song."))
			return
		}
	case search != "":
		results, err = database.SearchCardsByName(search)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to search songs."))
			return
		}
		if len(results) == 1 {
			card = results[0]
			results = nil
		}
	default:
		card, err = database.GetRandomCard()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Failed to pick a song."))
			return
		}
	}

	tmpl, err := parseChrome("html/pages/body/guess-test.html", nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to parse page template."))
		return
	}

	type data struct {
		gsApi.BasePageData
		Card            database.Card
		HasCard         bool
		Search          string
		Results         []database.Card
		ClaudeReady     bool
		ClaudeModel     string
		PromptBothJSON  template.JS
		PromptTitleJSON template.JS
	}

	promptBoth, _ := json.Marshal(guess.ClaudePromptPreview(
		false, card.Title, card.Artist, "@@TITLE_SAID@@", "@@ARTIST_SAID@@", "@@COMBINED@@",
	))
	promptTitle, _ := json.Marshal(guess.ClaudePromptPreview(
		true, card.Title, card.Artist, "@@TITLE_SAID@@", "", "",
	))

	_ = tmpl.ExecuteTemplate(w, "base", data{
		BasePageData:    basePageData,
		Card:            card,
		HasCard:         card.Id != uuid.Nil,
		Search:          search,
		Results:         results,
		ClaudeReady:     guess.ClaudeConfigured(),
		ClaudeModel:     guess.ClaudeModel(),
		PromptBothJSON:  template.JS(promptBoth),
		PromptTitleJSON: template.JS(promptTitle),
	})
}
