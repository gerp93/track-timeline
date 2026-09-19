package apiPages

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	gsApi "github.com/gerp93/gameshell-framework/api"

	"github.com/gerp93/track-timeline/guess"
	"github.com/gerp93/track-timeline/videocheck"
)

// statusProbeTimeout bounds each API check, so one hung service cannot leave
// the page spinning.
const statusProbeTimeout = 10 * time.Second

// The two probes are variables only so tests can stand in for the real APIs.
var (
	probeYouTube = videocheck.Ping
	probeClaude  = guess.PingClaude
)

// serviceStatus is what one check learned about one external API.
type serviceStatus struct {
	Name       string
	Purpose    string
	Configured bool
	// KeyVar is the environment variable to set when it is not configured.
	KeyVar string
	Up     bool
	// Latency is how long the real call took; zero when nothing was called.
	Latency time.Duration
	// UpNote is shown when the check succeeded, Problem when it failed.
	UpNote  string
	Problem string
}

// Status is the admin API Status page. It does no checking itself: the page
// asks StatusCheck to run both checks the moment it loads.
func Status(w http.ResponseWriter, r *http.Request) {
	basePageData := gsApi.GetBasePageData(r)
	if !basePageData.User.IsAdmin {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can check the APIs."))
		return
	}
	basePageData.PageTitle = basePageData.BrandName + " - API Status"

	tmpl, err := parseChrome("html/pages/body/status.html", nil)
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

// StatusCheck makes one real call to each external API and returns the
// results as an HTML fragment. It is a POST because it spends a little real
// quota and tokens each time it runs.
func StatusCheck(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can check the APIs."))
		return
	}

	statuses := runStatusChecks(r.Context())

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	writeStatuses(w, statuses, time.Now().UTC())
}

// runStatusChecks calls each configured API at the same time. A service
// without its key is reported as not configured and is not called at all.
func runStatusChecks(ctx context.Context) []serviceStatus {
	statuses := []serviceStatus{
		{
			Name:       "YouTube Data API",
			Purpose:    "Checks that songs are still playable and measures their length.",
			Configured: videocheck.Configured(),
			KeyVar:     "TRACK_TIMELINE_YT_API_KEY",
			UpNote:     fmt.Sprintf("The test spent %d unit of daily quota.", videocheck.VideosListQuotaUnits),
		},
		{
			Name:       "Claude API",
			Purpose:    "Judges the title and artist guesses. The word matcher only steps in if this is down.",
			Configured: guess.ClaudeConfigured(),
			KeyVar:     "TRACK_TIMELINE_ANTHROPIC_API_KEY",
			UpNote:     fmt.Sprintf("Model %s answered.", guess.ClaudeModel()),
		},
	}
	probes := []func(context.Context) error{probeYouTube, probeClaude}

	var wg sync.WaitGroup
	for i := range statuses {
		if !statuses[i].Configured {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, statusProbeTimeout)
			defer cancel()

			start := time.Now()
			err := probes[i](probeCtx)
			statuses[i].Latency = time.Since(start)
			if err != nil {
				statuses[i].Problem = describeProbeError(err)
				return
			}
			statuses[i].Up = true
		}(i)
	}
	wg.Wait()

	return statuses
}

// describeProbeError turns an API error into a short sentence for the admin:
// the likely meaning first, then the service's own words, capped in length.
func describeProbeError(err error) string {
	raw := strings.TrimSpace(err.Error())
	if runes := []rune(raw); len(runes) > 300 {
		raw = string(runes[:300]) + "…"
	}

	lower := strings.ToLower(raw)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("Timed out after %d seconds.", int(statusProbeTimeout/time.Second))
	case errors.Is(err, videocheck.ErrQuotaExceeded):
		return videocheck.UserMessage(err)
	case strings.Contains(lower, "401"), strings.Contains(lower, "api key"), strings.Contains(lower, "authentication"):
		return "The API key was rejected. Details: " + raw
	case strings.Contains(lower, "429"), strings.Contains(lower, "rate limit"):
		return "The API is rate limiting requests. Details: " + raw
	}
	return raw
}

func writeStatuses(w io.Writer, statuses []serviceStatus, checkedAt time.Time) {
	needAttention := 0
	for _, status := range statuses {
		if !status.Up {
			needAttention++
		}
	}

	if needAttention == 0 {
		_, _ = fmt.Fprint(w, "<p class=\"guess-verdict-ok\"><strong>Everything is reachable.</strong></p>\n")
	} else {
		_, _ = fmt.Fprintf(w, "<p class=\"guess-verdict-no\"><strong>%d of %d need attention.</strong></p>\n", needAttention, len(statuses))
	}

	_, _ = fmt.Fprint(w, "<div class=\"guess-compare\">\n")
	for _, status := range statuses {
		_, _ = fmt.Fprintf(w, "<div><h3>%s</h3>\n<p>%s</p>\n", html.EscapeString(status.Name), html.EscapeString(status.Purpose))
		switch {
		case !status.Configured:
			_, _ = fmt.Fprintf(w, "<p class=\"guess-verdict-no\"><strong>Not configured.</strong></p>\n<p>Set %s on the server. Nothing was called.</p>\n",
				html.EscapeString(status.KeyVar))
		case status.Up:
			_, _ = fmt.Fprintf(w, "<p class=\"guess-verdict-ok\"><strong>Up.</strong> Answered in %d ms.</p>\n<p>%s</p>\n",
				status.Latency.Milliseconds(), html.EscapeString(status.UpNote))
		default:
			_, _ = fmt.Fprintf(w, "<p class=\"guess-verdict-no\"><strong>Down.</strong> Failed after %d ms.</p>\n<p>%s</p>\n",
				status.Latency.Milliseconds(), html.EscapeString(status.Problem))
		}
		_, _ = fmt.Fprint(w, "</div>\n")
	}
	_, _ = fmt.Fprint(w, "</div>\n")
	_, _ = fmt.Fprintf(w, "<p class=\"hint\">Checked at %s UTC.</p>\n", checkedAt.Format("15:04:05"))
}
