package apiTrackTimeline

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/static"
)

// currentCardView mirrors the anonymous struct GetCurrentCard passes to the
// template — kept local so a field rename in the handler breaks this test.
type currentCardView struct {
	database.CurrentCard
	Answer          database.CurrentCardAnswer
	Revealed        bool
	LobbyId         uuid.UUID
	GameStatus      string
	RoundPhase      string
	IsCurrentPlayer bool
	IsWinner        bool
	HasPlaced       bool
	HasGuessed      bool
	ReplayUsed      bool
	TokenCount      int
	GuessMode       string
}

func renderCurrentCard(t *testing.T, data currentCardView) string {
	t.Helper()
	tmpl, err := template.ParseFS(static.StaticFiles, "html/components/tracktimeline/current-card.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return buf.String()
}

func TestCurrentCardGameOverBanner(t *testing.T) {
	base := currentCardView{
		CurrentCard: database.CurrentCard{YouTubeVideoId: "abc123"},
		Answer: database.CurrentCardAnswer{
			Title:       "Purple Rain",
			Artist:      "Prince",
			ReleaseYear: 1984,
		},
		Revealed:   true,
		GameStatus: database.StatusFinished,
		RoundPhase: database.PhaseReveal,
		GuessMode:  database.GuessModeOff,
		LobbyId:    uuid.New(),
	}

	won := base
	won.IsWinner = true
	got := renderCurrentCard(t, won)
	if !strings.Contains(got, "YOU WON!") {
		t.Errorf("winner view missing YOU WON!: %s", got)
	}
	if strings.Contains(got, "YOU LOST!") {
		t.Errorf("winner view unexpectedly has YOU LOST!")
	}
	if strings.Contains(got, "1984") || strings.Contains(got, "Prince") || strings.Contains(got, "Purple Rain") {
		t.Errorf("winner view still shows the last song metadata: %s", got)
	}
	if !strings.Contains(got, "tt-record") {
		t.Errorf("winner view dropped the vinyl graphic")
	}

	lost := base
	lost.IsWinner = false
	got = renderCurrentCard(t, lost)
	if !strings.Contains(got, "YOU LOST!") {
		t.Errorf("loser view missing YOU LOST!: %s", got)
	}
	if strings.Contains(got, "YOU WON!") {
		t.Errorf("loser view unexpectedly has YOU WON!")
	}
	if strings.Contains(got, "1984") || strings.Contains(got, "Prince") {
		t.Errorf("loser view still shows the last song metadata: %s", got)
	}
}

func TestCurrentCardWagerNotEnoughTokensCopy(t *testing.T) {
	got := renderCurrentCard(t, currentCardView{
		CurrentCard:     database.CurrentCard{YouTubeVideoId: "abc123"},
		GameStatus:      database.StatusActive,
		RoundPhase:      database.PhaseListening,
		IsCurrentPlayer: true,
		HasPlaced:       false,
		TokenCount:      2,
		GuessMode:       database.GuessModeOff,
		LobbyId:         uuid.New(),
	})
	if !strings.Contains(got, "not enough tokens") {
		t.Errorf("exact-year wager form missing 'not enough tokens' decorator: %s", got)
	}
	if !strings.Contains(got, `id="tt-year-wager-error"`) {
		t.Errorf("exact-year wager form missing tt-year-wager-error element")
	}
}
