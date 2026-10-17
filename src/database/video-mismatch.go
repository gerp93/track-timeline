package database

import (
	"database/sql"
	"errors"
	"log"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/gerp93/track-timeline/guess"
)

// Percent bars under which a card's title/artist is considered missing from the
// title YouTube gives its video. The title bar is the stricter signal: a song
// can be uploaded by a label channel and still name itself, but a video that
// never says the song's name is almost never that song. The artist bar is lower
// because plenty of correct uploads carry the artist only as the channel name
// or not at all ("Heroes (2017 Remaster)" from a topic channel).
const (
	mismatchTitleBelow  = 60
	mismatchArtistBelow = 40
)

// SetVideoTitle records what YouTube calls a card's video, pinned to the video
// id it was read from so a later link edit makes it stale on its own.
func SetVideoTitle(cardId uuid.UUID, videoId, title, channel string) error {
	sqlString := `
		INSERT INTO TRACK_TIMELINE_CARD_VIDEO_STATUS(
			CARD_ID, AVAILABLE, YOUTUBE_TITLE, YOUTUBE_CHANNEL, YOUTUBE_TITLE_VIDEO_ID
		)
		VALUES (?, 1, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			YOUTUBE_TITLE = VALUES(YOUTUBE_TITLE),
			YOUTUBE_CHANNEL = VALUES(YOUTUBE_CHANNEL),
			YOUTUBE_TITLE_VIDEO_ID = VALUES(YOUTUBE_TITLE_VIDEO_ID)
	`
	return execute(sqlString, cardId, title, channel, videoId)
}

// DismissVideoMismatch records that an admin looked at a card's current video
// and it is the right song after all. Tied to the video id, so changing the
// link brings the card back for another look.
func DismissVideoMismatch(cardId uuid.UUID) error {
	sqlString := `
		UPDATE TRACK_TIMELINE_CARD_VIDEO_STATUS AS S
			JOIN CARD AS C ON C.ID = S.CARD_ID
		SET S.MISMATCH_OK_VIDEO_ID = C.YOUTUBE_VIDEO_ID
		WHERE S.CARD_ID = ?
	`
	return execute(sqlString, cardId)
}

// VideoMismatchCard is one row of the Library Mismatches tab: the song as the
// deck authors it beside what YouTube calls the video linked to it.
type VideoMismatchCard struct {
	CardId         uuid.UUID
	Title          string
	Artist         string
	ReleaseYear    sql.NullInt32
	DeckId         uuid.UUID
	DeckName       string
	YouTubeVideoId string
	VideoTitle     string
	VideoChannel   string
	TitlePercent   float64
	ArtistPercent  float64
}

// Score orders the list worst-first. The title counts double: a missing title
// is the stronger sign of a wrong video.
func (c VideoMismatchCard) Score() float64 {
	return (2*c.TitlePercent + c.ArtistPercent) / 3
}

// TitleBad and ArtistBad say which side of the comparison fell under its bar,
// so the page can highlight the part that looks wrong.
func (c VideoMismatchCard) TitleBad() bool  { return c.TitlePercent < mismatchTitleBelow }
func (c VideoMismatchCard) ArtistBad() bool { return c.ArtistPercent < mismatchArtistBelow }

// ListVideoMismatches returns every playable, not-yet-flagged card whose video
// title does not look like its song, worst first, optionally narrowed by a
// title/artist/deck/video-title search. Scored in Go rather than SQL: the
// comparison is the same fuzzy word matching the guess judge uses, and the
// whole library is small enough to score per request.
//
// Cards already unavailable, awaiting recheck or marked incorrect are left out
// -- they are on the Dead Videos tab already -- as are cards whose current video
// an admin dismissed, and cards with no title recorded for their current link
// (those get one on the next check).
func ListVideoMismatches(search string) ([]VideoMismatchCard, error) {
	sqlString := `
		SELECT C.ID, C.TITLE, C.ARTIST, C.RELEASE_YEAR, C.DECK_ID, D.NAME, C.YOUTUBE_VIDEO_ID,
			S.YOUTUBE_TITLE, COALESCE(S.YOUTUBE_CHANNEL, '')
		FROM CARD AS C
			JOIN TRACK_TIMELINE_CARD_VIDEO_STATUS AS S ON S.CARD_ID = C.ID
			JOIN DECK AS D ON D.ID = C.DECK_ID
		WHERE C.YOUTUBE_VIDEO_ID IS NOT NULL
			AND S.YOUTUBE_TITLE IS NOT NULL
			AND S.YOUTUBE_TITLE_VIDEO_ID = C.YOUTUBE_VIDEO_ID
			AND (S.MISMATCH_OK_VIDEO_ID IS NULL OR S.MISMATCH_OK_VIDEO_ID <> C.YOUTUBE_VIDEO_ID)
			AND S.AVAILABLE = 1
			AND S.AWAITING_VALIDATION = 0
			AND S.INCORRECT_VIDEO = 0
	`
	rows, err := query(sqlString)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	needle := strings.ToLower(strings.TrimSpace(search))
	result := make([]VideoMismatchCard, 0)
	for rows.Next() {
		var card VideoMismatchCard
		if err := rows.Scan(
			&card.CardId, &card.Title, &card.Artist, &card.ReleaseYear, &card.DeckId, &card.DeckName,
			&card.YouTubeVideoId, &card.VideoTitle, &card.VideoChannel,
		); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}

		match := guess.CompareToVideo(card.Title, card.Artist, card.VideoTitle, card.VideoChannel)
		card.TitlePercent = match.TitlePercent
		card.ArtistPercent = match.ArtistPercent
		if !card.TitleBad() && !card.ArtistBad() {
			continue
		}
		haystack := strings.ToLower(card.Title + " " + card.Artist + " " + card.DeckName + " " + card.VideoTitle)
		if needle != "" && !strings.Contains(haystack, needle) {
			continue
		}
		result = append(result, card)
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Score() != result[j].Score() {
			return result[i].Score() < result[j].Score()
		}
		return strings.ToLower(result[i].Title) < strings.ToLower(result[j].Title)
	})
	return result, nil
}

// CountVideoMismatches is the Mismatches tab badge.
func CountVideoMismatches() (int, error) {
	cards, err := ListVideoMismatches("")
	if err != nil {
		return 0, err
	}
	return len(cards), nil
}

// CountVideosWithoutTitle reports how many cards with a video link have no
// YouTube title recorded for that link yet, so the tab can say the list is
// still filling in rather than letting "no mismatches" read as "all clear".
func CountVideosWithoutTitle() (int, error) {
	sqlString := `
		SELECT COUNT(*)
		FROM CARD AS C
			LEFT JOIN TRACK_TIMELINE_CARD_VIDEO_STATUS AS S ON S.CARD_ID = C.ID
		WHERE C.YOUTUBE_VIDEO_ID IS NOT NULL
			AND (S.CARD_ID IS NULL OR S.YOUTUBE_TITLE_VIDEO_ID IS NULL OR S.YOUTUBE_TITLE_VIDEO_ID <> C.YOUTUBE_VIDEO_ID)
	`
	rows, err := query(sqlString)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		if err := rows.Scan(&count); err != nil {
			log.Println(err)
			return 0, errors.New("failed to scan row in query results")
		}
	}
	return count, nil
}
