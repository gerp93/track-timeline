// Package videocheck verifies whether YouTube videos are still playable and
// how long they run, via the YouTube Data API v3. It knows nothing about
// cards, decks, or the database — it is handed video ids and returns facts
// about them.
package videocheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// checkTimeout bounds the whole check, including every batch. This is an
// explicit admin action rather than something in gameplay's critical path,
// so it can afford to be generous.
const checkTimeout = 15 * time.Second

// maxIdsPerRequest is the YouTube Data API v3 videos.list batch cap.
const maxIdsPerRequest = 50

// VideosListQuotaUnits is the cost of one videos.list HTTP call (any batch size).
const VideosListQuotaUnits = 1

var (
	quotaMu       sync.Mutex
	quotaRecorder func(units int)
)

// SetQuotaRecorder installs a callback invoked after each successful API
// request with the units that call consumed. main wires this to the local
// TRACK_TIMELINE_YT_QUOTA_DAY estimate — Google never returns remaining quota
// on these responses.
func SetQuotaRecorder(fn func(units int)) {
	quotaMu.Lock()
	defer quotaMu.Unlock()
	quotaRecorder = fn
}

func recordQuota(units int) {
	quotaMu.Lock()
	fn := quotaRecorder
	quotaMu.Unlock()
	if fn != nil && units > 0 {
		fn(units)
	}
}

type videosListResponse struct {
	Items []struct {
		Id     string `json:"id"`
		Status struct {
			Embeddable *bool `json:"embeddable"`
		} `json:"status"`
		ContentDetails struct {
			Duration string `json:"duration"`
		} `json:"contentDetails"`
	} `json:"items"`
}

// errorResponse is the Google API error envelope. Only the human-readable
// message (and quota reason codes) are wanted: the full body is a wall of
// JSON that ends up in logs and, worse, in UI text when handlers surface it.
type errorResponse struct {
	Error struct {
		Message string `json:"message"`
		Errors  []struct {
			Reason string `json:"reason"`
		} `json:"errors"`
	} `json:"error"`
}

// ErrQuotaExceeded is returned when Google rejects a call for daily quota.
var ErrQuotaExceeded = errors.New("YouTube daily API quota exceeded")

var htmlTagPattern = regexp.MustCompile(`<[^>]+>`)

// apiError turns a non-200 response body into a one-line reason, falling back
// to a bare status code when the body isn't the shape we expect.
func apiError(statusCode int, body []byte) error {
	var parsed errorResponse
	if err := json.Unmarshal(body, &parsed); err == nil {
		for _, item := range parsed.Error.Errors {
			switch item.Reason {
			case "quotaExceeded", "dailyLimitExceeded":
				return ErrQuotaExceeded
			}
		}
		if message := strings.TrimSpace(parsed.Error.Message); message != "" {
			message = htmlTagPattern.ReplaceAllString(message, "")
			message = strings.TrimSpace(message)
			if strings.Contains(strings.ToLower(message), "quota") {
				return ErrQuotaExceeded
			}
			return fmt.Errorf("YouTube rejected the request: %s", message)
		}
	}
	return fmt.Errorf("YouTube returned an unexpected status (%d)", statusCode)
}

// UserMessage turns a videocheck error into a short sentence for admin UI.
func UserMessage(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrQuotaExceeded) {
		return "YouTube daily API quota exceeded."
	}
	msg := strings.TrimSpace(err.Error())
	msg = htmlTagPattern.ReplaceAllString(msg, "")
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "Failed to talk to YouTube."
	}
	runes := []rune(msg)
	if unicode.IsLower(runes[0]) {
		runes[0] = unicode.ToUpper(runes[0])
		msg = string(runes)
	}
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}

// VideoInfo is what one check learned about a single video.
type VideoInfo struct {
	Available bool
	// DurationSeconds is 0 when unknown — either the video wasn't returned at
	// all, or YouTube reported no real length (live streams and unstarted
	// premieres come back as "P0D"). Callers must treat 0 as "no usable
	// duration", never as a zero-length video.
	DurationSeconds int
}

// iso8601DurationPattern matches the subset of ISO 8601 durations YouTube
// actually emits for videos (no years or months — a video is never that
// long, and those units have no fixed second count anyway).
var iso8601DurationPattern = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// parseISO8601Duration converts YouTube's "PT4M13S" form into seconds,
// returning 0 for anything it cannot parse. Go's time.ParseDuration does not
// understand this format ("4m13s" is its own, different syntax), so this is
// hand-rolled rather than delegated.
func parseISO8601Duration(value string) int {
	match := iso8601DurationPattern.FindStringSubmatch(value)
	if match == nil {
		return 0
	}
	number := func(raw string) int {
		parsed, _ := strconv.Atoi(raw)
		return parsed
	}
	return number(match[1])*86400 + number(match[2])*3600 + number(match[3])*60 + number(match[4])
}

// CheckAvailable reports, for each of videoIds, whether the video is still
// public and embeddable and how long it runs. A video id that YouTube does
// not return at all (deleted, private) or that comes back with embedding
// explicitly disabled is unavailable; anything else is available.
//
// Duration rides along on the same request: videos.list costs one quota unit
// per call regardless of how many parts it asks for, so fetching
// contentDetails alongside status is free.
//
// Requires TRACK_TIMELINE_YT_API_KEY. Unlike guess.Adjudicate, this returns
// an error rather than degrading — there is no sensible fallback for "is
// this specific video still up," so a failed check should be visibly
// retried rather than silently reported as all-fine or all-broken.
func CheckAvailable(ctx context.Context, videoIds []string) (map[string]VideoInfo, error) {
	apiKey := os.Getenv("TRACK_TIMELINE_YT_API_KEY")
	if apiKey == "" {
		return nil, errors.New("TRACK_TIMELINE_YT_API_KEY is not set")
	}

	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	result := make(map[string]VideoInfo, len(videoIds))
	client := &http.Client{}

	for start := 0; start < len(videoIds); start += maxIdsPerRequest {
		end := min(start+maxIdsPerRequest, len(videoIds))
		batch := videoIds[start:end]

		for _, id := range batch {
			result[id] = VideoInfo{}
		}

		reqUrl := "https://www.googleapis.com/youtube/v3/videos?" + url.Values{
			"part": {"status,contentDetails"},
			"id":   {strings.Join(batch, ",")},
			"key":  {apiKey},
		}.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqUrl, nil)
		if err != nil {
			return nil, err
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("youtube data api request failed: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			return nil, apiError(resp.StatusCode, body)
		}

		var parsed videosListResponse
		err = json.NewDecoder(resp.Body).Decode(&parsed)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to parse youtube data api response: %w", err)
		}
		recordQuota(VideosListQuotaUnits)

		for _, item := range parsed.Items {
			result[item.Id] = VideoInfo{
				Available:       item.Status.Embeddable == nil || *item.Status.Embeddable,
				DurationSeconds: parseISO8601Duration(item.ContentDetails.Duration),
			}
		}
	}

	log.Printf("videocheck: checked %d video(s)", len(videoIds))

	return result, nil
}
