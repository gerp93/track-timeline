package apiPages

import (
	"html/template"
	"math"
	"net/http"
	"strconv"

	gsApi "github.com/gerp93/gameshell-framework/api"
	gsApiPages "github.com/gerp93/gameshell-framework/api/pages"
	gsDatabase "github.com/gerp93/gameshell-framework/database"
	gsStatic "github.com/gerp93/gameshell-framework/static"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
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

	tmpl, err := parseChrome("html/pages/body/home.html", nil)
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
	page := 1
	for key, val := range r.URL.Query() {
		switch key {
		case "search":
			search = val[0]
		case "page":
			if parsed, err := strconv.Atoi(val[0]); err == nil {
				page = parsed
			}
		}
	}
	if page < 1 {
		page = 1
	}

	rowCount, err := database.CountCardsInDeck(deckId, search)
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

	cards, err := database.SearchCardsInDeck(deckId, search, page)
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
		Deck       gsDatabase.Deck
		Cards      []database.Card
		Categories []database.Category
		Search     string
		Page       int
		LastPage   int
		RowCount   int
	}

	_ = tmpl.ExecuteTemplate(w, "base", data{
		BasePageData: basePageData,
		Deck:         deck,
		Cards:        cards,
		Categories:   categories,
		Search:       search,
		Page:         page,
		LastPage:     lastPage,
		RowCount:     rowCount,
	})
}
