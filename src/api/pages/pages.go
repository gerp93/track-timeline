package apiPages

import (
	"html/template"
	"net/http"

	gsApi "github.com/gerp93/gameshell-framework/api"
	gsStatic "github.com/gerp93/gameshell-framework/static"

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
