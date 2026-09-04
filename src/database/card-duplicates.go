package database

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/gerp93/track-timeline/guess"
	"github.com/google/uuid"
)

// DuplicateCard is one song in a suspected-duplicate cluster.
type DuplicateCard struct {
	CardId         uuid.UUID
	Title          string
	Artist         string
	DeckId         uuid.UUID
	DeckName       string
	YouTubeVideoId string
	ReleaseYear    int // 0 when NULL
	CreatedOnDate  time.Time
}

// DuplicateGroup is a set of cards that share a normalized title+artist key
// and still have at least one undismissed pair among them.
type DuplicateGroup struct {
	Key   string
	Cards []DuplicateCard
}

// ListDuplicateCandidates returns every card with its deck name for grouping.
func ListDuplicateCandidates() ([]DuplicateCard, error) {
	rows, err := query(`
		SELECT C.ID, C.TITLE, C.ARTIST, C.DECK_ID, D.NAME, COALESCE(C.YOUTUBE_VIDEO_ID, ''),
			COALESCE(C.RELEASE_YEAR, 0), C.CREATED_ON_DATE
		FROM CARD C
		INNER JOIN DECK D ON D.ID = C.DECK_ID
		ORDER BY C.ARTIST, C.TITLE, D.NAME
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]DuplicateCard, 0)
	for rows.Next() {
		var c DuplicateCard
		if err := rows.Scan(
			&c.CardId, &c.Title, &c.Artist, &c.DeckId, &c.DeckName,
			&c.YouTubeVideoId, &c.ReleaseYear, &c.CreatedOnDate,
		); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		out = append(out, c)
	}
	return out, nil
}

// ListDismissedDuplicatePairs returns every admin-dismissed card pair, keyed
// as ordered (low, high) UUID bytes.
func ListDismissedDuplicatePairs() (map[[2]uuid.UUID]bool, error) {
	rows, err := query(`
		SELECT CARD_ID_A, CARD_ID_B
		FROM TRACK_TIMELINE_CARD_DUPLICATE_DISMISS
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[[2]uuid.UUID]bool)
	for rows.Next() {
		var a, b uuid.UUID
		if err := rows.Scan(&a, &b); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		out[orderedPairKey(a, b)] = true
	}
	return out, nil
}

// DismissDuplicateGroup marks every unordered pair among cardIds as not a
// duplicate so the cluster leaves the duplicates list.
func DismissDuplicateGroup(cardIds []uuid.UUID) error {
	if len(cardIds) < 2 {
		return errors.New("need at least two songs to dismiss")
	}
	seen := make(map[uuid.UUID]bool, len(cardIds))
	unique := make([]uuid.UUID, 0, len(cardIds))
	for _, id := range cardIds {
		if id == uuid.Nil || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	if len(unique) < 2 {
		return errors.New("need at least two songs to dismiss")
	}

	for i := 0; i < len(unique); i++ {
		for j := i + 1; j < len(unique); j++ {
			a, b := orderedPair(unique[i], unique[j])
			if err := execute(`
				INSERT IGNORE INTO TRACK_TIMELINE_CARD_DUPLICATE_DISMISS (
					CARD_ID_A, CARD_ID_B
				) VALUES (?, ?)
			`, a, b); err != nil {
				return err
			}
		}
	}
	return nil
}

// BuildDuplicateGroups clusters candidates by CanonicalKey, then drops pairs
// the admin already marked as not duplicates. Connected components of size
// ≥2 remain. search filters groups whose title, artist, or deck matches
// (case-insensitive substring).
func BuildDuplicateGroups(cards []DuplicateCard, dismissed map[[2]uuid.UUID]bool, search string) []DuplicateGroup {
	if dismissed == nil {
		dismissed = map[[2]uuid.UUID]bool{}
	}

	byKey := map[string][]DuplicateCard{}
	for _, c := range cards {
		key := guess.CanonicalKey(c.Title, c.Artist)
		if key == "" {
			continue
		}
		byKey[key] = append(byKey[key], c)
	}

	search = strings.TrimSpace(strings.ToLower(search))
	groups := make([]DuplicateGroup, 0)
	for key, members := range byKey {
		if len(members) < 2 {
			continue
		}
		for _, component := range undismissedComponents(members, dismissed) {
			if len(component) < 2 {
				continue
			}
			if search != "" && !groupMatchesSearch(component, search) {
				continue
			}
			groups = append(groups, DuplicateGroup{Key: key, Cards: component})
		}
	}

	// Stable-ish order: title+artist key, then deck names within each group
	// are already whatever order members arrived; sort groups by first card.
	sortDuplicateGroups(groups)
	return groups
}

// CountDuplicateGroups is how many undismissed clusters still need a look
// on the Library duplicates tab.
func CountDuplicateGroups() (int, error) {
	candidates, err := ListDuplicateCandidates()
	if err != nil {
		return 0, err
	}
	dismissed, err := ListDismissedDuplicatePairs()
	if err != nil {
		return 0, err
	}
	return len(BuildDuplicateGroups(candidates, dismissed, "")), nil
}

// BuildDismissedDuplicateGroups clusters cards that share at least one
// admin-dismissed pair (connected components on the dismissal graph).
// search filters the same way as BuildDuplicateGroups.
func BuildDismissedDuplicateGroups(cards []DuplicateCard, dismissed map[[2]uuid.UUID]bool, search string) []DuplicateGroup {
	if len(dismissed) == 0 {
		return nil
	}

	byId := make(map[uuid.UUID]DuplicateCard, len(cards))
	for _, c := range cards {
		byId[c.CardId] = c
	}

	// Union-find over every card that appears in a dismissal, ignoring pairs
	// where either card was deleted (CASCADE should clear those rows, but
	// stay resilient if a race leaves an orphan).
	parent := map[uuid.UUID]uuid.UUID{}
	var find func(uuid.UUID) uuid.UUID
	find = func(id uuid.UUID) uuid.UUID {
		p, ok := parent[id]
		if !ok {
			parent[id] = id
			return id
		}
		if p != id {
			parent[id] = find(p)
		}
		return parent[id]
	}
	union := func(a, b uuid.UUID) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	for pair := range dismissed {
		if _, ok := byId[pair[0]]; !ok {
			continue
		}
		if _, ok := byId[pair[1]]; !ok {
			continue
		}
		union(pair[0], pair[1])
	}

	byRoot := map[uuid.UUID][]DuplicateCard{}
	for id := range parent {
		c, ok := byId[id]
		if !ok {
			continue
		}
		root := find(id)
		byRoot[root] = append(byRoot[root], c)
	}

	search = strings.TrimSpace(strings.ToLower(search))
	groups := make([]DuplicateGroup, 0)
	for _, members := range byRoot {
		if len(members) < 2 {
			continue
		}
		if search != "" && !groupMatchesSearch(members, search) {
			continue
		}
		key := guess.CanonicalKey(members[0].Title, members[0].Artist)
		groups = append(groups, DuplicateGroup{Key: key, Cards: members})
	}
	sortDuplicateGroups(groups)
	return groups
}

// CountDismissedDuplicateGroups is how many clusters sit on the "not
// duplicates" management list.
func CountDismissedDuplicateGroups() (int, error) {
	candidates, err := ListDuplicateCandidates()
	if err != nil {
		return 0, err
	}
	dismissed, err := ListDismissedDuplicatePairs()
	if err != nil {
		return 0, err
	}
	return len(BuildDismissedDuplicateGroups(candidates, dismissed, "")), nil
}

// UndismissDuplicateGroup removes every dismissal row among cardIds so the
// cluster can reappear on the suspected-duplicates list.
func UndismissDuplicateGroup(cardIds []uuid.UUID) error {
	if len(cardIds) < 2 {
		return errors.New("need at least two songs to restore")
	}
	seen := make(map[uuid.UUID]bool, len(cardIds))
	unique := make([]uuid.UUID, 0, len(cardIds))
	for _, id := range cardIds {
		if id == uuid.Nil || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	if len(unique) < 2 {
		return errors.New("need at least two songs to restore")
	}

	for i := 0; i < len(unique); i++ {
		for j := i + 1; j < len(unique); j++ {
			a, b := orderedPair(unique[i], unique[j])
			if err := execute(`
				DELETE FROM TRACK_TIMELINE_CARD_DUPLICATE_DISMISS
				WHERE CARD_ID_A = ? AND CARD_ID_B = ?
			`, a, b); err != nil {
				return err
			}
		}
	}
	return nil
}

func groupMatchesSearch(cards []DuplicateCard, search string) bool {
	for _, c := range cards {
		if strings.Contains(strings.ToLower(c.Title), search) ||
			strings.Contains(strings.ToLower(c.Artist), search) ||
			strings.Contains(strings.ToLower(c.DeckName), search) {
			return true
		}
	}
	return false
}

func sortDuplicateGroups(groups []DuplicateGroup) {
	sort.Slice(groups, func(i, j int) bool {
		return lessDuplicateCard(groups[i].Cards[0], groups[j].Cards[0])
	})
}

func lessDuplicateCard(a, b DuplicateCard) bool {
	if a.Artist != b.Artist {
		return a.Artist < b.Artist
	}
	if a.Title != b.Title {
		return a.Title < b.Title
	}
	return a.DeckName < b.DeckName
}

// undismissedComponents returns connected components where an edge exists
// between two cards iff that pair has not been dismissed.
func undismissedComponents(members []DuplicateCard, dismissed map[[2]uuid.UUID]bool) [][]DuplicateCard {
	n := len(members)
	adj := make([][]int, n)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if dismissed[orderedPairKey(members[i].CardId, members[j].CardId)] {
				continue
			}
			adj[i] = append(adj[i], j)
			adj[j] = append(adj[j], i)
		}
	}

	seen := make([]bool, n)
	out := make([][]DuplicateCard, 0)
	for i := 0; i < n; i++ {
		if seen[i] {
			continue
		}
		stack := []int{i}
		seen[i] = true
		comp := make([]DuplicateCard, 0)
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			comp = append(comp, members[cur])
			for _, next := range adj[cur] {
				if seen[next] {
					continue
				}
				seen[next] = true
				stack = append(stack, next)
			}
		}
		out = append(out, comp)
	}
	return out
}

func orderedPair(a, b uuid.UUID) (uuid.UUID, uuid.UUID) {
	if bytes.Compare(a[:], b[:]) <= 0 {
		return a, b
	}
	return b, a
}

func orderedPairKey(a, b uuid.UUID) [2]uuid.UUID {
	lo, hi := orderedPair(a, b)
	return [2]uuid.UUID{lo, hi}
}

// exactFieldKey groups cards that match 100% on title, artist, year, and
// video id (deck ignored). Empty video ids are excluded — those are not a
// shared recording to auto-prune.
func exactFieldKey(c DuplicateCard) string {
	if c.YouTubeVideoId == "" {
		return ""
	}
	return fmt.Sprintf("%s\x00%s\x00%d\x00%s", c.Title, c.Artist, c.ReleaseYear, c.YouTubeVideoId)
}

// exactTitleArtistYearKey groups cards that match 100% on title, artist, and
// year (video and deck ignored).
func exactTitleArtistYearKey(c DuplicateCard) string {
	if c.Title == "" || c.Artist == "" {
		return ""
	}
	return fmt.Sprintf("%s\x00%s\x00%d", c.Title, c.Artist, c.ReleaseYear)
}

// latestIdsByKey returns newer card ids in each key-group of size ≥2, keeping
// the oldest (by CreatedOnDate, then card id). keyFn returning "" skips a card.
func latestIdsByKey(cards []DuplicateCard, keyFn func(DuplicateCard) string) []uuid.UUID {
	byKey := map[string][]DuplicateCard{}
	for _, c := range cards {
		key := keyFn(c)
		if key == "" {
			continue
		}
		byKey[key] = append(byKey[key], c)
	}

	toDelete := make([]uuid.UUID, 0)
	for _, members := range byKey {
		if len(members) < 2 {
			continue
		}
		sort.Slice(members, func(i, j int) bool {
			if !members[i].CreatedOnDate.Equal(members[j].CreatedOnDate) {
				return members[i].CreatedOnDate.Before(members[j].CreatedOnDate)
			}
			return bytes.Compare(members[i].CardId[:], members[j].CardId[:]) < 0
		})
		for _, newer := range members[1:] {
			toDelete = append(toDelete, newer.CardId)
		}
	}
	return toDelete
}

// ExactDuplicateLatestIds returns card ids that are newer copies in an
// exact title/artist/year/video match group (deck may differ). The oldest
// card in each group is kept.
func ExactDuplicateLatestIds(cards []DuplicateCard) []uuid.UUID {
	return latestIdsByKey(cards, exactFieldKey)
}

// ExactTitleArtistLatestIds returns newer copies in an exact title+artist+year
// match group (video and deck may differ).
func ExactTitleArtistLatestIds(cards []DuplicateCard) []uuid.UUID {
	return latestIdsByKey(cards, exactTitleArtistYearKey)
}

func deleteLatestByKey(keyFn func(DuplicateCard) string) (int, error) {
	cards, err := ListDuplicateCandidates()
	if err != nil {
		return 0, err
	}
	ids := latestIdsByKey(cards, keyFn)
	for _, id := range ids {
		if err := DeleteCard(id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// DeleteExactDuplicateLatests deletes every newer card in exact-match groups
// (same title, artist, year, and video id). Returns how many were deleted.
func DeleteExactDuplicateLatests() (int, error) {
	return deleteLatestByKey(exactFieldKey)
}

// DeleteExactTitleArtistLatests deletes newer cards that match an older one
// exactly on title, artist, and year.
func DeleteExactTitleArtistLatests() (int, error) {
	return deleteLatestByKey(exactTitleArtistYearKey)
}
