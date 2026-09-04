package videocheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Search costs 100 quota units per call (YouTube Data API v3 search.list).
const SearchQuotaUnits = 100

// SearchHit is one candidate from a music-video search.
type SearchHit struct {
	VideoId      string
	Title        string
	ChannelTitle string
}

type searchListResponse struct {
	Items []struct {
		Id struct {
			VideoId string `json:"videoId"`
		} `json:"id"`
		Snippet struct {
			Title        string `json:"title"`
			ChannelTitle string `json:"channelTitle"`
		} `json:"snippet"`
	} `json:"items"`
}

// SearchMusicVideo looks up an embeddable YouTube video for artist + title.
// Prefers official/VEVO/Topic-style channels when present among the top hits;
// otherwise returns the first result. Costs SearchQuotaUnits.
func SearchMusicVideo(ctx context.Context, artist string, title string) (SearchHit, error) {
	apiKey := os.Getenv("TRACK_TIMELINE_YT_API_KEY")
	if apiKey == "" {
		return SearchHit{}, errors.New("TRACK_TIMELINE_YT_API_KEY is not set")
	}

	artist = strings.TrimSpace(artist)
	title = strings.TrimSpace(title)
	if artist == "" || title == "" {
		return SearchHit{}, errors.New("artist and title are required to search")
	}

	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	query := artist + " " + title + " official music video"
	reqUrl := "https://www.googleapis.com/youtube/v3/search?" + url.Values{
		"part":            {"snippet"},
		"type":            {"video"},
		"videoEmbeddable": {"true"},
		"videoCategoryId": {"10"},
		"maxResults":      {"5"},
		"q":               {query},
		"key":             {apiKey},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqUrl, nil)
	if err != nil {
		return SearchHit{}, err
	}

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return SearchHit{}, fmt.Errorf("youtube search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return SearchHit{}, apiError(resp.StatusCode, body)
	}
	recordQuota(SearchQuotaUnits)

	var parsed searchListResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return SearchHit{}, fmt.Errorf("failed to parse youtube search response: %w", err)
	}
	if len(parsed.Items) == 0 {
		return SearchHit{}, errors.New("no YouTube results for that song")
	}

	hits := make([]SearchHit, 0, len(parsed.Items))
	for _, item := range parsed.Items {
		if item.Id.VideoId == "" {
			continue
		}
		hits = append(hits, SearchHit{
			VideoId:      item.Id.VideoId,
			Title:        item.Snippet.Title,
			ChannelTitle: item.Snippet.ChannelTitle,
		})
	}
	if len(hits) == 0 {
		return SearchHit{}, errors.New("no YouTube results for that song")
	}

	return pickPreferredHit(hits), nil
}

func pickPreferredHit(hits []SearchHit) SearchHit {
	for _, hit := range hits {
		channel := strings.ToLower(hit.ChannelTitle)
		if strings.Contains(channel, "vevo") ||
			strings.Contains(channel, "official") ||
			strings.HasSuffix(channel, " - topic") ||
			strings.Contains(channel, "topic") {
			return hit
		}
	}
	return hits[0]
}
