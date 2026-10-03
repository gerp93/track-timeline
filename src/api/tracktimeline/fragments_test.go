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
	Answer             database.CurrentCardAnswer
	Revealed           bool
	LobbyId            uuid.UUID
	GameStatus         string
	RoundPhase         string
	IsCurrentPlayer    bool
	IsWinner           bool
	HasPlaced          bool
	HasGuessed         bool
	GuessResultText    string
	ReplayUsed         bool
	PlaybackMode       string
	CanChallenge       bool
	MaxChallengeTokens int
	TokenCount         int
	Economy            database.Economy
	IsRoom             bool
	IsHostDisplay      bool
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

// TestCurrentCardTurnPlayerHasGuessButton guards against the guess UI
// regressing back to bundling the turn player's guess into their placement
// with no way to lock it in on its own (playtest fix): the turn player's own
// branch must render a real submit button, posting to the same /guess
// endpoint every other player uses, with its fields preserved across a
// lobby-wide refresh via hx-preserve.
func TestCurrentCardTurnPlayerHasGuessButton(t *testing.T) {
	got := renderCurrentCard(t, currentCardView{
		CurrentCard:     database.CurrentCard{YouTubeVideoId: "abc123"},
		GameStatus:      database.StatusActive,
		RoundPhase:      database.PhaseListening,
		IsCurrentPlayer: true,
		HasGuessed:      false,
		LobbyId:         uuid.New(),
	})
	if !strings.Contains(got, `class="guess-form turn-player-guess"`) {
		t.Fatalf("turn player guess form missing: %s", got)
	}
	if !strings.Contains(got, "/guess\"") {
		t.Errorf("turn player guess form does not post to /guess: %s", got)
	}
	if !strings.Contains(got, `<button type="submit" class="btn-small">`) {
		t.Errorf("turn player guess form missing a submit button: %s", got)
	}
	if !strings.Contains(got, `id="tt-guess-fields" class="guess-fields" hx-preserve="true"`) {
		t.Errorf("turn player guess fields missing hx-preserve: %s", got)
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
		LobbyId:         uuid.New(),
	})
	if !strings.Contains(got, "Not enough tokens") {
		t.Errorf("exact-year wager form missing 'not enough tokens' decorator: %s", got)
	}
	if !strings.Contains(got, `id="tt-year-wager-error"`) {
		t.Errorf("exact-year wager form missing tt-year-wager-error element")
	}
}

// TestCurrentCardAlreadyGuessedShowsResult guards the playtest fix where a
// player who already guessed only ever saw a generic "You have already
// guessed this song." line, with their actual verdict and token odds
// (describeStoredGuessForPlayer, round.go) gone the moment the one-time
// "alert:" broadcast that carried it scrolled away.
func TestCurrentCardAlreadyGuessedShowsResult(t *testing.T) {
	withResult := currentCardView{
		CurrentCard:     database.CurrentCard{YouTubeVideoId: "abc123"},
		GameStatus:      database.StatusActive,
		RoundPhase:      database.PhaseListening,
		HasGuessed:      true,
		GuessResultText: "title right (100% match), artist right (100% match) You earned 2 tokens!",
		LobbyId:         uuid.New(),
	}
	got := renderCurrentCard(t, withResult)
	if !strings.Contains(got, "You earned 2 tokens!") {
		t.Errorf("expected the stored guess result to render in place of the generic message: %s", got)
	}
	if strings.Contains(got, "You have already guessed this song.") {
		t.Errorf("generic message should not render once a real result is available: %s", got)
	}

	withoutResult := withResult
	withoutResult.GuessResultText = ""
	got = renderCurrentCard(t, withoutResult)
	if !strings.Contains(got, "You have already guessed this song.") {
		t.Errorf("expected the generic fallback when no guess result is available: %s", got)
	}
}

// timelineView mirrors the anonymous struct GetTimeline passes to
// timeline.html — kept local so a field rename in the handler breaks this
// test, same reasoning as currentCardView above.
type timelineView struct {
	Timelines         []database.PlayerTimeline
	LobbyId           uuid.UUID
	GameStatus        string
	RoundPhase        string
	CanPlace          bool
	CanSteal          bool
	TokenCount        int
	CardsToWin        int
	InLead            bool
	Economy           database.Economy
	CurrentPlayerName string
	GuessedCount      int
	ActivePlayerCount int
	IsRoom            bool
	IsHostDisplay     bool
}

func renderTimeline(t *testing.T, data timelineView) string {
	t.Helper()
	tmpl, err := template.New("timeline.html").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
	}).ParseFS(static.StaticFiles, "html/components/tracktimeline/timeline.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return buf.String()
}

// TestTimelineShowsTokenCountPerPlayer guards the playtest fix that put each
// player's token balance next to their own name on the board, not just the
// viewer's own in the header badge.
func TestTimelineShowsTokenCountPerPlayer(t *testing.T) {
	got := renderTimeline(t, timelineView{
		GameStatus: database.StatusActive,
		RoundPhase: database.PhaseListening,
		Timelines: []database.PlayerTimeline{
			{PlayerName: "Alice", TokenCount: 5, IsMe: true},
			{PlayerName: "Bob", TokenCount: 2},
		},
	})
	if !strings.Contains(got, `<span class="player-tokens" title="Alice's tokens">5`) {
		t.Errorf("Alice's token count missing from her row: %s", got)
	}
	if !strings.Contains(got, `<span class="player-tokens" title="Bob's tokens">2`) {
		t.Errorf("Bob's token count missing from his row: %s", got)
	}
}

func TestCurrentCardHostDisplayHidesWatchTvHint(t *testing.T) {
	phone := renderCurrentCard(t, currentCardView{
		CurrentCard:     database.CurrentCard{YouTubeVideoId: "abc123"},
		GameStatus:      database.StatusActive,
		RoundPhase:      database.PhaseListening,
		IsCurrentPlayer: false,
		HasGuessed:      false,
		IsRoom:          true,
		IsHostDisplay:   false,
		LobbyId:         uuid.New(),
	})
	if !strings.Contains(phone, "Watch the TV") {
		t.Fatalf("room phone spectator should see Watch the TV hint: %s", phone)
	}
	if strings.Contains(phone, "tt-record") || strings.Contains(phone, "platter") {
		t.Fatalf("room phone should not render the turntable: %s", phone)
	}

	host := renderCurrentCard(t, currentCardView{
		CurrentCard:     database.CurrentCard{YouTubeVideoId: "abc123"},
		GameStatus:      database.StatusActive,
		RoundPhase:      database.PhaseListening,
		IsCurrentPlayer: false,
		HasGuessed:      false,
		IsRoom:          true,
		IsHostDisplay:   true,
		LobbyId:         uuid.New(),
	})
	if strings.Contains(host, "Watch the TV") {
		t.Fatalf("host TV should not tell viewers to watch the TV: %s", host)
	}
}

func TestCurrentCardRoomPhoneTurnOmitsTurntable(t *testing.T) {
	got := renderCurrentCard(t, currentCardView{
		CurrentCard:     database.CurrentCard{YouTubeVideoId: "abc123"},
		GameStatus:      database.StatusActive,
		RoundPhase:      database.PhaseListening,
		IsCurrentPlayer: true,
		HasGuessed:      false,
		IsRoom:          true,
		IsHostDisplay:   false,
		TokenCount:      5,
		Economy:         database.CurrentEconomy(),
		LobbyId:         uuid.New(),
	})
	if strings.Contains(got, "tt-record") || strings.Contains(got, "tt-visualizer") {
		t.Fatalf("room phone turn UI should omit vinyl chrome: %s", got)
	}
	if !strings.Contains(got, `id="tt-play-pause-btn"`) {
		t.Fatalf("room phone turn UI missing Play: %s", got)
	}
	if !strings.Contains(got, "Lock guess") {
		t.Fatalf("room phone turn UI missing Lock guess: %s", got)
	}
	if !strings.Contains(got, `id="tt-guess-year-btn"`) || !strings.Contains(got, "Guess year") {
		t.Fatalf("room phone turn UI missing Guess year CTA: %s", got)
	}
	if !strings.Contains(got, `id="room-phone-place"`) {
		t.Fatalf("room phone turn UI missing place panel: %s", got)
	}
	if !strings.Contains(got, "You have 5 tokens to wager.") {
		t.Fatalf("room phone place panel should show the tokens available to wager: %s", got)
	}
	if !strings.Contains(got, `class="room-place-top"`) || strings.Contains(got, "room-place-hint") {
		t.Fatalf("room phone place panel should be Back + exact-year toggle only: %s", got)
	}
	if !strings.Contains(got, "Exact-year wager") {
		t.Fatalf("room phone place panel missing exact-year wager: %s", got)
	}
	// Exact year lives on the Guess-year screen, not beside listen controls.
	listenIdx := strings.Index(got, `id="room-phone-listen"`)
	placeIdx := strings.Index(got, `id="room-phone-place"`)
	exactIdx := strings.Index(got, `id="tt-use-exact-year"`)
	if listenIdx < 0 || placeIdx < 0 || exactIdx < 0 || !(listenIdx < placeIdx && placeIdx < exactIdx) {
		t.Fatalf("exact-year should be inside place panel after listen: listen=%d place=%d exact=%d", listenIdx, placeIdx, exactIdx)
	}
}

func TestTimelineRoomPhoneOwnOnlyWhenPlacing(t *testing.T) {
	waiting := renderTimeline(t, timelineView{
		IsRoom:        true,
		IsHostDisplay: false,
		GameStatus:    database.StatusActive,
		RoundPhase:    database.PhaseListening,
		CanPlace:      false,
		Timelines: []database.PlayerTimeline{
			{PlayerName: "Alice", TokenCount: 2, IsMe: true},
			{PlayerName: "Bob", TokenCount: 2},
		},
	})
	if strings.Contains(waiting, "Your timeline") || strings.Contains(waiting, "cassette-card") {
		t.Fatalf("room phone must hide timelines while not placing: %s", waiting)
	}
	if strings.Contains(waiting, "Bob") {
		t.Fatalf("room phone must never list other players: %s", waiting)
	}

	placing := renderTimeline(t, timelineView{
		IsRoom:        true,
		IsHostDisplay: false,
		GameStatus:    database.StatusActive,
		RoundPhase:    database.PhaseListening,
		CanPlace:      true,
		Timelines: []database.PlayerTimeline{
			{
				PlayerName: "Alice",
				TokenCount: 2,
				IsMe:       true,
				Timeline:   []database.TimelineCard{{ReleaseYear: 1990, Artist: "A", Title: "T"}},
			},
			{PlayerName: "Bob", TokenCount: 2},
		},
	})
	if !strings.Contains(placing, "Your timeline") {
		t.Fatalf("room phone placing view missing own timeline: %s", placing)
	}
	if !strings.Contains(placing, "Place here") {
		t.Fatalf("room phone placing view slots should be labelled: %s", placing)
	}
	if !strings.Contains(placing, "drop-zone") {
		t.Fatalf("room phone placing view missing drop zones: %s", placing)
	}
	if strings.Contains(placing, "Bob") {
		t.Fatalf("room phone placing view leaked other player: %s", placing)
	}
}

// TestTimelineBoardBannerNamesCurrentPlayerAndGuessCount guards the
// persistent "what's going on" status line: it must name the actual player
// on turn (not a generic placeholder) and show a live guessed-so-far count,
// and it must not show the guess count once the round has reached reveal.
func TestTimelineBoardBannerNamesCurrentPlayerAndGuessCount(t *testing.T) {
	got := renderTimeline(t, timelineView{
		GameStatus:        database.StatusActive,
		RoundPhase:        database.PhaseListening,
		CanPlace:          false,
		CurrentPlayerName: "Priya",
		GuessedCount:      2,
		ActivePlayerCount: 4,
	})
	if !strings.Contains(got, "Waiting for Priya to place the song.") {
		t.Errorf("banner does not name the current player: %s", got)
	}
	if !strings.Contains(got, "2 of 4 guessed so far") {
		t.Errorf("banner missing live guessed-so-far count: %s", got)
	}

	revealed := renderTimeline(t, timelineView{
		GameStatus:        database.StatusActive,
		RoundPhase:        database.PhaseReveal,
		CurrentPlayerName: "Priya",
	})
	if strings.Contains(revealed, "guessed so far") {
		t.Errorf("guessed-so-far count should not render at reveal: %s", revealed)
	}
}

// TestTimelineBuyButtonCostAndLeadRestriction guards the raised Buy cost (2
// -> 3) and the new in-the-lead restriction: the button's label/confirm text
// must reflect BuyCardCost rather than a hardcoded "2", and it must be
// disabled with an explicit reason when the viewer is the strict leader.
func TestTimelineBuyButtonCostAndLeadRestriction(t *testing.T) {
	notLeader := renderTimeline(t, timelineView{
		GameStatus: database.StatusActive,
		RoundPhase: database.PhaseListening,
		Economy:    database.Economy{BuyCardCost: 3},
		InLead:     false,
		CardsToWin: 10,
		Timelines:  []database.PlayerTimeline{{PlayerName: "Alice", TokenCount: 6, IsMe: true}},
	})
	if !strings.Contains(notLeader, "Buy (3)") {
		t.Errorf("Buy button does not show BuyCardCost: %s", notLeader)
	}
	if !strings.Contains(notLeader, "Spend 3 tokens") {
		t.Errorf("Buy confirm text does not show BuyCardCost: %s", notLeader)
	}
	if strings.Contains(notLeader, "disabled") {
		t.Errorf("Buy should not be disabled for a non-leader with enough tokens: %s", notLeader)
	}

	leader := renderTimeline(t, timelineView{
		GameStatus: database.StatusActive,
		RoundPhase: database.PhaseListening,
		Economy:    database.Economy{BuyCardCost: 3},
		InLead:     true,
		CardsToWin: 10,
		Timelines:  []database.PlayerTimeline{{PlayerName: "Alice", TokenCount: 6, IsMe: true}},
	})
	if !strings.Contains(leader, "You can't buy while you're in or tied for the lead") {
		t.Errorf("Buy button missing in-the-lead tooltip: %s", leader)
	}
	if !strings.Contains(leader, "disabled") {
		t.Errorf("Buy button should be disabled for the strict leader: %s", leader)
	}
}

// TestCurrentCardShowsEconomyPrices guards against costs being typed into the
// template: the Restart/Skip labels, their confirm text and their disabled
// state must all follow the economy handed to the fragment. The prices here are
// deliberately unlike the real ones.
func TestCurrentCardShowsEconomyPrices(t *testing.T) {
	eco := database.Economy{GuessTokensPerPart: 3, ReplayCost: 7, SkipCost: 9}
	got := renderCurrentCard(t, currentCardView{
		CurrentCard:     database.CurrentCard{YouTubeVideoId: "abc123"},
		GameStatus:      database.StatusActive,
		RoundPhase:      database.PhaseListening,
		IsCurrentPlayer: true,
		TokenCount:      8, // enough to restart (7), not to skip (9)
		Economy:         eco,
		LobbyId:         uuid.New(),
	})
	for _, want := range []string{"Restart (7)", "Skip (9)", `data-cost="7"`, `data-cost="9"`, "Spend 7 tokens", "Spend 9 tokens"} {
		if !strings.Contains(got, want) {
			t.Errorf("fragment missing %q: %s", want, got)
		}
	}
	if strings.Count(got, `data-no-tokens="1"`) != 1 {
		t.Errorf("only Skip (9 > 8 tokens) should be disabled for cost, got %d disabled: %s", strings.Count(got, `data-no-tokens="1"`), got)
	}
	if !strings.Contains(got, "3 tokens for the song name, 3 tokens for the artist") {
		t.Errorf("guess hint should state the per-part reward: %s", got)
	}
}

// Guessing is one rule now: both boxes are always offered, to the turn player
// and to everyone else.
func TestCurrentCardAlwaysOffersBothGuessBoxes(t *testing.T) {
	for name, isTurn := range map[string]bool{"turn player": true, "other player": false} {
		got := renderCurrentCard(t, currentCardView{
			CurrentCard:     database.CurrentCard{YouTubeVideoId: "abc123"},
			GameStatus:      database.StatusActive,
			RoundPhase:      database.PhaseListening,
			IsCurrentPlayer: isTurn,
			LobbyId:         uuid.New(),
		})
		if !strings.Contains(got, `name="guessTitle"`) || !strings.Contains(got, `name="guessArtist"`) {
			t.Errorf("%s should be offered both the song name and artist boxes: %s", name, got)
		}
	}
}

// New Clip is offered to the player on turn while they are still listening,
// priced from the economy — and not at all when the lobby plays the whole song,
// where there is no other part to pick.
func TestCurrentCardNewClipButton(t *testing.T) {
	base := currentCardView{
		CurrentCard:     database.CurrentCard{YouTubeVideoId: "abc123"},
		LobbyId:         uuid.New(),
		GameStatus:      database.StatusActive,
		RoundPhase:      database.PhaseListening,
		IsCurrentPlayer: true,
		PlaybackMode:    database.PlaybackSample,
		TokenCount:      5,
		Economy:         database.CurrentEconomy(),
	}

	got := renderCurrentCard(t, base)
	if !strings.Contains(got, `id="tt-newclip-btn"`) {
		t.Fatalf("expected a New Clip button for the player on turn: %s", got)
	}
	if !strings.Contains(got, "New Clip (2)") || !strings.Contains(got, "/new-clip") {
		t.Errorf("New Clip button should show its price and post to /new-clip: %s", got)
	}

	whole := base
	whole.PlaybackMode = database.PlaybackFull
	if strings.Contains(renderCurrentCard(t, whole), "tt-newclip-btn") {
		t.Error("a whole-song lobby has no other clip to pick, so no New Clip button")
	}

	placed := base
	placed.HasPlaced = true
	if strings.Contains(renderCurrentCard(t, placed), "tt-newclip-btn") {
		t.Error("New Clip must not be offered once a placement is locked in")
	}
}

// Everyone — not just the player on turn — gets the Challenge button between
// rounds; it is absent whenever the server says a challenge is not possible.
func TestCurrentCardChallengeButton(t *testing.T) {
	base := currentCardView{
		CurrentCard:        database.CurrentCard{YouTubeVideoId: "abc123"},
		LobbyId:            uuid.New(),
		GameStatus:         database.StatusActive,
		RoundPhase:         database.PhaseListening,
		IsCurrentPlayer:    false,
		CanChallenge:       true,
		MaxChallengeTokens: database.MaxChallengeTokens,
		Economy:            database.CurrentEconomy(),
	}

	got := renderCurrentCard(t, base)
	if !strings.Contains(got, `id="tt-challenge-btn"`) {
		t.Fatalf("a player who is not on turn should still get the Challenge button: %s", got)
	}
	if !strings.Contains(got, `data-max-tokens="10"`) {
		t.Errorf("the button should carry the token cap for the form: %s", got)
	}

	onTurn := base
	onTurn.IsCurrentPlayer = true
	if !strings.Contains(renderCurrentCard(t, onTurn), `id="tt-challenge-btn"`) {
		t.Error("the player on turn should get the Challenge button too")
	}

	notAllowed := base
	notAllowed.CanChallenge = false
	if strings.Contains(renderCurrentCard(t, notAllowed), "tt-challenge-btn") {
		t.Error("no Challenge button when the server says one is not possible")
	}
}

// Two songs from the same year have nothing to choose between them, so the
// board offers no slot there — in the online board and the room phone alike.
func TestTimelineNoSlotBetweenSameYearCards(t *testing.T) {
	cards := []database.TimelineCard{
		{ReleaseYear: 1990, Artist: "A", Title: "One", SameYearAsNext: true},
		{ReleaseYear: 1990, Artist: "B", Title: "Two"},
		{ReleaseYear: 1995, Artist: "C", Title: "Three"},
	}
	for name, room := range map[string]bool{"online": false, "room phone": true} {
		got := renderTimeline(t, timelineView{
			IsRoom:     room,
			GameStatus: database.StatusActive,
			RoundPhase: database.PhaseListening,
			CanPlace:   true,
			Timelines:  []database.PlayerTimeline{{PlayerName: "Alice", IsMe: true, Timeline: cards}},
		})
		// Before the first, after the second 1990, after 1995 — not between the 1990s.
		if n := strings.Count(got, `class="drop-zone"`); n != 3 {
			t.Errorf("%s: %d slots, want 3 (none between the two 1990 cards): %s", name, n, got)
		}
		if strings.Contains(got, `{"position": 1}`) {
			t.Errorf("%s: offers position 1, between the two 1990 cards: %s", name, got)
		}
	}
}

// Nothing to lock until something is typed or spoken: the room phone's Lock
// guess button starts hidden (room-phone.js reveals it).
func TestCurrentCardRoomPhoneLockGuessStartsHidden(t *testing.T) {
	got := renderCurrentCard(t, currentCardView{
		CurrentCard:     database.CurrentCard{YouTubeVideoId: "abc123"},
		GameStatus:      database.StatusActive,
		RoundPhase:      database.PhaseListening,
		IsCurrentPlayer: true,
		IsRoom:          true,
		TokenCount:      3,
		Economy:         database.CurrentEconomy(),
		LobbyId:         uuid.New(),
	})
	if !strings.Contains(got, `id="tt-lock-guess" class="btn-primary" hidden`) {
		t.Fatalf("Lock guess should start hidden: %s", got)
	}
	// Initial form: Hold to speak only; Re-record waits for a spoken guess.
	if !strings.Contains(got, `id="tt-re-record" class="btn-secondary" style="display:none"`) {
		t.Fatalf("Re-record should start hidden: %s", got)
	}
	if strings.Contains(got, `id="tt-hold-mic" class="btn-primary" style="display:none"`) {
		t.Fatalf("Hold to speak should start visible: %s", got)
	}
}
