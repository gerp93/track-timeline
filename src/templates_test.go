package main

import (
	"bytes"
	"database/sql"
	"html/template"
	"testing"

	gsApi "github.com/gerp93/gameshell-framework/api"
	gsStatic "github.com/gerp93/gameshell-framework/static"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/static"
)

func TestRulesAndSettingsTemplates(t *testing.T) {
	tmpl := template.New("base.html")
	var err error
	tmpl, err = tmpl.ParseFS(gsStatic.StaticFiles, "html/pages/base.html")
	if err != nil {
		t.Fatalf("parse base.html: %v", err)
	}

	tmpl, err = tmpl.ParseFS(static.StaticFiles,
		"html/pages/body/track-timeline.html",
		"html/components/tracktimeline/rules.html",
	)
	if err != nil {
		t.Fatalf("parse track-timeline.html and rules.html: %v", err)
	}

	type pageData struct {
		gsApi.BasePageData
		Lobby            database.Lobby
		Game             database.Game
		Decks            []database.DeckInfo
		DrawPileCount    int
		YearRanges       []database.YearRange
		TurnTimerSeconds int
		WinnerName       string
	}

	data := pageData{
		BasePageData: gsApi.BasePageData{
			PageTitle: "Game Lobby",
		},
		Lobby: database.Lobby{
			Id:          uuid.New(),
			Name:        "Rock Legends",
			HasPassword: true,
			Message:     sql.NullString{String: "Welcome to the game!", Valid: true},
		},
		Game: database.Game{
			Id:                uuid.New(),
			CardsToWin:        10,
			StartingTokens:    2,
			GuessMode:         database.GuessModeBoth,
			GuessMatchPercent: 70,
			GuessJudge:        database.GuessJudgeClaude,
			PlaybackMode:      database.PlaybackSample,
			ClipSeconds:       20,
		},
		Decks: []database.DeckInfo{
			{Name: "Classic Rock", TotalCount: 150, RemainingCount: 120},
			{Name: "90s Hits", TotalCount: 100, RemainingCount: 80},
		},
		DrawPileCount: 200,
		YearRanges: []database.YearRange{
			{FromYear: 1970, ToYear: 1999},
		},
		TurnTimerSeconds: 60,
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", data); err != nil {
		t.Fatalf("execute track-timeline.html template: %v", err)
	}

	out := buf.String()
	requiredSnippets := []string{
		"rules-dialog",
		"lobby-settings-dialog",
		"How to Play &amp; Rules",
		"Lobby Startup Settings",
		"Rock Legends",
		"Classic Rock",
		"First to",
		"Sample (random clip, intro skipped)",
		"Both title and artist required",
		"Claude AI Judge",
	}

	for _, req := range requiredSnippets {
		if !bytes.Contains([]byte(out), []byte(req)) {
			t.Errorf("expected rendered output to contain %q, but was missing", req)
		}
	}
}

func TestAboutPageWithRulesTemplate(t *testing.T) {
	tmpl := template.New("base.html")
	var err error
	tmpl, err = tmpl.ParseFS(gsStatic.StaticFiles, "html/pages/base.html")
	if err != nil {
		t.Fatalf("parse base.html: %v", err)
	}

	tmpl, err = tmpl.ParseFS(static.StaticFiles,
		"html/pages/body/about.html",
		"html/components/tracktimeline/rules.html",
	)
	if err != nil {
		t.Fatalf("parse about.html and rules.html: %v", err)
	}

	type aboutData struct {
		gsApi.BasePageData
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", aboutData{BasePageData: gsApi.BasePageData{PageTitle: "About"}}); err != nil {
		t.Fatalf("execute about.html template: %v", err)
	}

	out := buf.String()
	requiredSnippets := []string{
		"Track Timeline Rules",
		"Round Flow: How Each Turn Works",
		"Timeline Placement Rules",
		"Tokens &amp; The Economy",
		"Challenging &amp; Stealing",
		"Illustrated Examples",
		"A Note on Fair Play",
	}

	for _, req := range requiredSnippets {
		if !bytes.Contains([]byte(out), []byte(req)) {
			t.Errorf("expected rendered about page to contain %q, but was missing", req)
		}
	}
}
