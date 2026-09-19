package apiPages

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gerp93/track-timeline/videocheck"
)

// stubProbes swaps the two real API calls for fakes for the length of a test.
func stubProbes(t *testing.T, youtube, claude func(context.Context) error) {
	t.Helper()
	oldYouTube, oldClaude := probeYouTube, probeClaude
	probeYouTube, probeClaude = youtube, claude
	t.Cleanup(func() { probeYouTube, probeClaude = oldYouTube, oldClaude })
}

func setKeys(t *testing.T, youtube, claude string) {
	t.Helper()
	t.Setenv("TRACK_TIMELINE_YT_API_KEY", youtube)
	t.Setenv("TRACK_TIMELINE_ANTHROPIC_API_KEY", claude)
	t.Setenv("ANTHROPIC_API_KEY", "")
}

func TestStatusChecksReportEachServiceAndRunTogether(t *testing.T) {
	setKeys(t, "yt-key", "claude-key")
	const each = 250 * time.Millisecond
	stubProbes(t,
		func(context.Context) error { time.Sleep(each); return nil },
		func(context.Context) error { time.Sleep(each); return errors.New("POST /v1/messages: 401 Unauthorized") },
	)

	start := time.Now()
	statuses := runStatusChecks(context.Background())
	elapsed := time.Since(start)

	if len(statuses) != 2 {
		t.Fatalf("expected two services, got %d", len(statuses))
	}
	if !statuses[0].Up || statuses[0].Problem != "" {
		t.Errorf("YouTube should be up: %+v", statuses[0])
	}
	if statuses[1].Up || !strings.Contains(statuses[1].Problem, "API key was rejected") {
		t.Errorf("Claude should be down with a key problem: %+v", statuses[1])
	}
	if statuses[0].Latency < each || statuses[1].Latency < each {
		t.Errorf("latency should reflect the real call: %v and %v", statuses[0].Latency, statuses[1].Latency)
	}
	if elapsed >= 2*each {
		t.Errorf("the two checks should run at the same time, took %v", elapsed)
	}
}

func TestStatusChecksDoNotCallAServiceWithoutItsKey(t *testing.T) {
	setKeys(t, "", "")
	stubProbes(t,
		func(context.Context) error { t.Error("YouTube must not be called without a key"); return nil },
		func(context.Context) error { t.Error("Claude must not be called without a key"); return nil },
	)

	for _, status := range runStatusChecks(context.Background()) {
		if status.Configured || status.Up || status.Latency != 0 {
			t.Errorf("%s should read as not configured and untouched: %+v", status.Name, status)
		}
		if status.KeyVar == "" {
			t.Errorf("%s should say which variable to set", status.Name)
		}
	}
}

func TestProbeErrorsAreExplained(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"quota", videocheck.ErrQuotaExceeded, "quota"},
		{"timeout", context.DeadlineExceeded, "Timed out"},
		{"bad key", errors.New("401 Unauthorized {\"error\":\"invalid x-api-key\"}"), "API key was rejected"},
		{"rate limit", errors.New("429 Too Many Requests"), "rate limiting"},
		{"anything else", errors.New("dial tcp: no such host"), "no such host"},
	}
	for _, c := range cases {
		if got := describeProbeError(c.err); !strings.Contains(strings.ToLower(got), strings.ToLower(c.want)) {
			t.Errorf("%s: got %q, want it to mention %q", c.name, got, c.want)
		}
	}

	long := describeProbeError(errors.New(strings.Repeat("x", 5000)))
	if len([]rune(long)) > 310 {
		t.Errorf("a long error should be cut short, got %d runes", len([]rune(long)))
	}
}

func TestStatusFragmentSummarisesAndEscapes(t *testing.T) {
	var buf bytes.Buffer
	writeStatuses(&buf, []serviceStatus{
		{Name: "Good", Configured: true, Up: true, Latency: 12 * time.Millisecond, UpNote: "fine"},
		{Name: "Bad", Configured: true, Latency: 34 * time.Millisecond, Problem: "<script>alert(1)</script>"},
		{Name: "Missing", KeyVar: "SOME_KEY"},
	}, time.Date(2026, 9, 19, 12, 30, 5, 0, time.UTC))
	out := buf.String()

	for _, want := range []string{
		"2 of 3 need attention",
		"Up.</strong> Answered in 12 ms",
		"Down.</strong> Failed after 34 ms",
		"Not configured.",
		"Set SOME_KEY on the server",
		"Checked at 12:30:05 UTC",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fragment missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<script>") {
		t.Errorf("an error message must be escaped, got: %s", out)
	}

	buf.Reset()
	writeStatuses(&buf, []serviceStatus{{Name: "A", Configured: true, Up: true}, {Name: "B", Configured: true, Up: true}}, time.Now())
	if !strings.Contains(buf.String(), "Everything is reachable") {
		t.Errorf("all-up fragment should say so: %s", buf.String())
	}
}
