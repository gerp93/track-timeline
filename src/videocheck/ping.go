package videocheck

import (
	"context"
	"os"
)

// pingVideoId is a long-lived, embeddable video used only to give the API
// something real to answer. Whether it is still playable is irrelevant: the
// ping asks whether the API is reachable and accepts the key, not about the
// video.
const pingVideoId = "dQw4w9WgXcQ"

// Configured reports whether the YouTube Data API key is set.
func Configured() bool {
	return os.Getenv("TRACK_TIMELINE_YT_API_KEY") != ""
}

// Ping makes one real videos.list call, the same request the library check
// makes, and returns nil when the API answered with a success. It spends
// VideosListQuotaUnits of the daily quota and records it like any other call.
func Ping(ctx context.Context) error {
	_, err := CheckAvailable(ctx, []string{pingVideoId})
	return err
}
