package videocheck

import (
	"context"
	"testing"
	"time"
)

// TestLivePing makes one real YouTube Data API call (one unit of daily quota).
// Skipped without a key. Run with -v to see it:
//
//	go test ./videocheck -run TestLivePing -v
func TestLivePing(t *testing.T) {
	if !Configured() {
		t.Skip("TRACK_TIMELINE_YT_API_KEY is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	err := Ping(ctx)
	t.Logf("YouTube ping took %v, err=%v", time.Since(start), err)
	if err != nil {
		t.Errorf("the YouTube API did not answer a ping: %s", UserMessage(err))
	}
}
