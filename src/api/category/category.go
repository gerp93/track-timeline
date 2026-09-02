package apiCategory

import (
	"net/http"
	"strings"

	gsApi "github.com/gerp93/gameshell-framework/api"
	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/database"
)

// requireAdmin writes its own response and returns false for non-admins.
// gsApi.MiddlewareForAPIs only enforces that someone is logged in, so every
// admin-only API endpoint has to make this check itself.
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !gsApi.UserIsAdmin(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Only an admin can manage genres."))
		return false
	}
	return true
}

func Create(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("A name is required."))
		return
	}

	if _, err := database.CreateCategory(name); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to create genre. That name may already be in use."))
		return
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Genre created."))
}

// DeleteReassign deletes a genre after moving its cards to another one. It is a
// POST rather than a DELETE because it carries a form body naming the
// destination, and Go's ParseForm only reads the body for POST/PUT/PATCH.
func DeleteReassign(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to parse form."))
		return
	}

	categoryId, err := uuid.Parse(r.PathValue("categoryId"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid genre."))
		return
	}

	reassignToId, err := uuid.Parse(strings.TrimSpace(r.FormValue("reassignToId")))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Choose a genre to move the cards to."))
		return
	}

	if err := database.DeleteCategoryReassigning(categoryId, reassignToId); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(err.Error()))
		return
	}

	w.Header().Add("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Genre deleted."))
}
