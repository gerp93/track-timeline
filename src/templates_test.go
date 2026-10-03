package main

import (
	"bytes"
	"database/sql"
	"html/template"
	"strings"
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
		DrawPileTooltip  string
		YearRanges       []database.YearRange
		TurnTimerSeconds int
		WinnerName       string
		Economy          database.Economy

		PlaybackOptions []database.PlaybackOption
		MinClipSeconds  int
		MaxClipSeconds  int
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
			PlaybackMode:      database.PlaybackSample,
			ClipSeconds:       20,
		},
		Decks: []database.DeckInfo{
			{Name: "Classic Rock", TotalCount: 150, RemainingCount: 120},
			{Name: "90s Hits", TotalCount: 100, RemainingCount: 80},
		},
		DrawPileCount: 200,
		DrawPileTooltip: "200 songs remaining: 150 new, 50 repeated",
		YearRanges: []database.YearRange{
			{FromYear: 1970, ToYear: 1999},
		},
		TurnTimerSeconds: 60,
		Economy:          database.CurrentEconomy(),

		PlaybackOptions: database.PlaybackOptions(),
		MinClipSeconds:  database.MinClipSeconds,
		MaxClipSeconds:  database.MaxClipSeconds,
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
		`id="tt-live-settings"`,
		`<option value="sample" selected="selected">Random clip from the middle</option>`,
		`name="clipSeconds"`,
		`hx-put="/api/track-timeline/`,
		"Guess Rewards",
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
		Economy database.Economy
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", aboutData{BasePageData: gsApi.BasePageData{PageTitle: "About"}, Economy: database.CurrentEconomy()}); err != nil {
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

// TestRulesFollowTheEconomy guards against prices being typed into the rules
// text: render them with prices unlike the real ones and every one must show up.
func TestRulesFollowTheEconomy(t *testing.T) {
	tmpl := template.New("base.html")
	tmpl, err := tmpl.ParseFS(gsStatic.StaticFiles, "html/pages/base.html")
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

	eco := database.Economy{
		GuessTokensPerPart: 3,
		MaxGuessTokens:     6,
		SkipCost:           11,
		ReplayCost:         12,
		StealCost:          13,
		BuyCardCost:        14,
	}
	type aboutData struct {
		gsApi.BasePageData
		Economy database.Economy
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", aboutData{BasePageData: gsApi.BasePageData{PageTitle: "About"}, Economy: eco}); err != nil {
		t.Fatalf("execute about.html template: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"3 tokens for the song name and 3 tokens for the artist, up to 6 tokens per round",
		"spend 11 tokens to pass a difficult song",
		"spend 12 tokens to listen to the song clip a second time",
		"Spend 13 tokens to challenge",
		"Spend 14 tokens at any time",
		"at least 13 tokens can click",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rules text should follow the economy, but is missing %q", want)
		}
	}
	// None of the real prices should be left over as literals.
	for _, literal := range []string{"spend 1 token", "Spend 5 tokens", "-5</span>"} {
		if strings.Contains(out, literal) {
			t.Errorf("rules still contain the hardcoded %q", literal)
		}
	}
}
