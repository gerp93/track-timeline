package videocheck

import (
	"errors"
	"strings"
	"testing"
)

func TestAPIError(t *testing.T) {
	// The real body YouTube returns for an expired key — the whole point is
	// that this collapses to one line instead of reaching a player's screen
	// as a wall of JSON.
	expiredKey := []byte(`{
  "error": {
    "code": 400,
    "message": "API key expired. Please renew the API key.",
    "errors": [{"message": "API key expired. Please renew the API key.", "domain": "global", "reason": "badRequest"}],
    "status": "INVALID_ARGUMENT"
  }
}`)

	got := apiError(400, expiredKey).Error()
	if !strings.Contains(got, "API key expired") {
		t.Errorf("expected the API's own reason to survive, got %q", got)
	}
	if strings.Contains(got, "{") || strings.Contains(got, "\n") {
		t.Errorf("expected a single readable line, got %q", got)
	}

	quotaBody := []byte(`{
  "error": {
    "code": 403,
    "message": "The request cannot be completed because you have exceeded your <a href=\"/youtube/v3/getting-started#quota\">quota</a>.",
    "errors": [{"message": "The request cannot be completed because you have exceeded your <a href=\"/youtube/v3/getting-started#quota\">quota</a>.", "domain": "youtube.quota", "reason": "quotaExceeded"}],
    "status": "PERMISSION_DENIED"
  }
}`)
	if err := apiError(403, quotaBody); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("expected ErrQuotaExceeded, got %v", err)
	}
	if msg := UserMessage(ErrQuotaExceeded); msg != "YouTube daily API quota exceeded." {
		t.Errorf("UserMessage(quota) = %q", msg)
	}

	// A body that isn't the expected envelope must still produce something
	// usable rather than an empty reason.
	fallback := apiError(503, []byte("<html>gateway timeout</html>")).Error()
	if !strings.Contains(fallback, "503") {
		t.Errorf("expected the status code in the fallback, got %q", fallback)
	}
}

func TestParseISO8601Duration(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  int
	}{
		{"minutes and seconds", "PT4M13S", 253},
		{"seconds only", "PT45S", 45},
		{"minutes only", "PT3M", 180},
		{"hours minutes seconds", "PT1H2M3S", 3723},
		{"hours only", "PT2H", 7200},
		// YouTube reports live streams and unstarted premieres this way. It
		// must come back as 0 (unknown), never mistaken for a real length.
		{"live stream", "P0D", 0},
		{"zero seconds", "PT0S", 0},
		{"empty", "", 0},
		{"not a duration", "4m13s", 0},
		{"years rejected rather than guessed at", "P1Y", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseISO8601Duration(tc.value); got != tc.want {
				t.Errorf("parseISO8601Duration(%q) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}
}
