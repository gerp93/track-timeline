package apiCard

import (
	"fmt"
	"net/http"

	gsApi "github.com/gerp93/gameshell-framework/api"
	"github.com/gerp93/track-timeline/database"
	"github.com/google/uuid"
)

// DismissDuplicateGroup records that every pair among the posted card ids is
// not a duplicate, then refreshes the library page.
func DismissDuplicateGroup(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can dismiss duplicate songs."))
		return
	}

	cardIds, ok := parseBulkCardIds(w, r)
	if !ok {
		return
	}
	if len(cardIds) < 2 {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Select at least two songs in a duplicate group."))
		return
	}

	unique := uniqueCardIds(cardIds)
	if len(unique) < 2 {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Select at least two songs in a duplicate group."))
		return
	}
	if err := database.DismissDuplicateGroup(unique); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to dismiss duplicate songs."))
		return
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Marked as not duplicates."))
}

// UndismissDuplicateGroup restores a previously dismissed cluster to the
// suspected-duplicates list.
func UndismissDuplicateGroup(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can restore duplicate songs."))
		return
	}

	cardIds, ok := parseBulkCardIds(w, r)
	if !ok {
		return
	}
	unique := uniqueCardIds(cardIds)
	if len(unique) < 2 {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Select at least two songs in a duplicate group."))
		return
	}
	if err := database.UndismissDuplicateGroup(unique); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to restore duplicate songs."))
		return
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Restored to suspected duplicates."))
}

// DeleteExactDuplicateLatests removes newer cards that match an older one
// exactly on title, artist, year, and YouTube id (any deck).
func DeleteExactDuplicateLatests(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can delete duplicate songs."))
		return
	}

	deleted, err := database.DeleteExactDuplicateLatests()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to delete exact duplicate copies."))
		return
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	if deleted == 0 {
		_, _ = w.Write([]byte("No exact duplicate copies to delete."))
		return
	}
	_, _ = w.Write([]byte(fmt.Sprintf("Deleted %d newer exact duplicate copy/copies.", deleted)))
}

// DeleteExactTitleArtistLatests removes newer cards that match an older one
// exactly on title, artist, and year (video may differ).
func DeleteExactTitleArtistLatests(w http.ResponseWriter, r *http.Request) {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can delete duplicate songs."))
		return
	}

	deleted, err := database.DeleteExactTitleArtistLatests()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to delete title/artist/year duplicate copies."))
		return
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	if deleted == 0 {
		_, _ = w.Write([]byte("No title/artist/year duplicate copies to delete."))
		return
	}
	_, _ = w.Write([]byte(fmt.Sprintf("Deleted %d newer title/artist/year duplicate copy/copies.", deleted)))
}

func uniqueCardIds(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
