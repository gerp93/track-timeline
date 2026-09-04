package apiCard

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	gsApi "github.com/gerp93/gameshell-framework/api"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
	"github.com/gerp93/track-timeline/videocheck"
)

// staleAfter is how long a video's last check is trusted before the admin
// "check stale" button on Dead Videos will include it again. The homepage
// auto-check uses the same window as its process-level debounce.
const staleAfter = 24 * time.Hour

var (
	autoCheckMu      sync.Mutex
	autoCheckCond    = sync.NewCond(&autoCheckMu)
	autoCheckRunning bool
	lastAutoCheck    time.Time
)

// RecordVideoCheck checks every given card's video against YouTube and
// records the result, returning how many came back unavailable.
func RecordVideoCheck(ctx context.Context, cardVideos []database.CardVideoId) (unavailableCount int, err error) {
	if len(cardVideos) == 0 {
		return 0, nil
	}

	videoIds := make([]string, len(cardVideos))
	for i, cv := range cardVideos {
		videoIds[i] = cv.YouTubeVideoId
	}

	info, err := videocheck.CheckAvailable(ctx, videoIds)
	if err != nil {
		return 0, err
	}

	for _, cv := range cardVideos {
		video := info[cv.YouTubeVideoId]
		if !video.Available {
			unavailableCount++
		}
		if err := database.SetVideoStatus(cv.CardId, video.Available, video.DurationSeconds); err != nil {
			return unavailableCount, err
		}
	}

	return unavailableCount, nil
}

// StaleVideoCutoff is the timestamp before which a check counts as stale.
func StaleVideoCutoff() time.Time {
	return time.Now().Add(-staleAfter)
}

func runStaleVideoCheck(ctx context.Context) (checked int, unavailable int, err error) {
	cardVideos, err := database.GetStaleCardVideoIds(StaleVideoCutoff())
	if err != nil {
		return 0, 0, err
	}
	if len(cardVideos) == 0 {
		return 0, 0, nil
	}
	unavailable, err = RecordVideoCheck(ctx, cardVideos)
	return len(cardVideos), unavailable, err
}

// CheckStaleVideos re-checks every song not verified within staleAfter
// (including never-checked and playable-without-duration). Admin-only.
func CheckStaleVideos(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can check videos against YouTube."))
		return
	}

	checked, unavailableCount, err := runStaleVideoCheck(r.Context())
	if err != nil {
		log.Println(err)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("Failed to check videos against YouTube. Try again shortly."))
		return
	}
	if checked == 0 {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Every song was checked within the last 24 hours."))
		return
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf(
		"Checked %d song(s); %d unavailable.",
		checked, unavailableCount,
	)))
}

// AutoCheckStaleVideos is the homepage hook: at most once per staleAfter per
// process it re-checks stale songs, then returns the Library Issues button
// HTML so HTMX can swap it in live. Concurrent home loads wait on the same
// run rather than starting a second YouTube sweep.
func AutoCheckStaleVideos(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can check videos against YouTube."))
		return
	}

	autoCheckMu.Lock()
	for autoCheckRunning {
		autoCheckCond.Wait()
	}
	shouldRun := lastAutoCheck.IsZero() || time.Since(lastAutoCheck) >= staleAfter
	if shouldRun {
		autoCheckRunning = true
	}
	autoCheckMu.Unlock()

	if shouldRun {
		defer func() {
			autoCheckMu.Lock()
			autoCheckRunning = false
			lastAutoCheck = time.Now()
			autoCheckCond.Broadcast()
			autoCheckMu.Unlock()
		}()
		_, _, err := runStaleVideoCheck(r.Context())
		if err != nil {
			log.Println(err)
		}
	}

	writeHomeLibraryIssuesButton(w)
}

// LibraryIssuesButton returns the home-page Library Issues control with a
// fresh count (no YouTube call).
func LibraryIssuesButton(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can view library issues."))
		return
	}
	writeHomeLibraryIssuesButton(w)
}

func writeHomeLibraryIssuesButton(w http.ResponseWriter) {
	count, err := database.CountLibraryIssues()
	if err != nil {
		log.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to count library issues."))
		return
	}
	label := "Library Issues"
	if count > 0 {
		label = fmt.Sprintf("Library Issues (%d)", count)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<a href="/videos"><button type="button">` + label + `</button></a>`))
}

// ResolveDeadVideos re-checks songs marked awaiting recheck (after Find/Save)
// against YouTube. Unavailable-only rows are left alone until their link is
// changed. CheckStaleVideos is the broader 24h sweep across every song.
func ResolveDeadVideos(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can resolve dead videos."))
		return
	}

	cardVideos, err := database.GetAwaitingRecheckVideoIds()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to get songs."))
		return
	}
	if len(cardVideos) == 0 {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("There are no songs awaiting recheck."))
		return
	}

	unavailableCount, err := RecordVideoCheck(r.Context(), cardVideos)
	if err != nil {
		log.Println(err)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("Failed to check videos against YouTube. Try again shortly."))
		return
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf(
		"Re-checked %d song(s); %d still unavailable.",
		len(cardVideos), unavailableCount,
	)))
}

// applyFoundVideo writes a search hit onto the card and marks it awaiting
// validation so it stays on the dead-videos list until Resolve confirms it.
func applyFoundVideo(card database.Card, hit videocheck.SearchHit) error {
	existingId, err := database.GetCardIdByVideo(card.DeckId, hit.VideoId)
	if err != nil {
		return err
	}
	if existingId != uuid.Nil && existingId != card.Id {
		return fmt.Errorf("another card in this deck already uses that video")
	}
	if err := database.UpdateCardYouTubeVideoId(card.Id, hit.VideoId); err != nil {
		return err
	}
	return database.MarkVideoAwaitingValidation(card.Id)
}
