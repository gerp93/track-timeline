package database

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// youTubeVideoIdPattern is YouTube's own id shape: 11 characters of URL-safe
// base64. Anchored, so a value that merely contains an id is rejected rather
// than silently truncated.
var youTubeVideoIdPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// ParseYouTubeVideoId accepts either a bare video id or any of the URL forms
// people actually paste — watch?v=, youtu.be/, /embed/, /shorts/, /live/, with
// or without a scheme — and returns the bare id.
//
// Authors will paste whatever the share button gave them, and a URL stored in
// the id column would fail at playback time rather than at authoring time,
// which is much later and much harder to diagnose.
func ParseYouTubeVideoId(input string) (string, error) {
	candidate := strings.TrimSpace(input)
	if candidate == "" {
		return "", errors.New("no video ID or URL provided")
	}

	if youTubeVideoIdPattern.MatchString(candidate) {
		return candidate, nil
	}

	// url.Parse only recognizes a host when there is a scheme.
	withScheme := candidate
	if !strings.Contains(candidate, "://") {
		withScheme = "https://" + candidate
	}

	parsed, err := url.Parse(withScheme)
	if err != nil {
		return "", errors.New("could not read that as a YouTube video ID or URL")
	}

	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	switch host {
	case "youtu.be":
		if id := firstPathSegment(parsed.Path); youTubeVideoIdPattern.MatchString(id) {
			return id, nil
		}
	case "youtube.com", "m.youtube.com", "music.youtube.com", "youtube-nocookie.com":
		if id := parsed.Query().Get("v"); youTubeVideoIdPattern.MatchString(id) {
			return id, nil
		}
		segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(segments) >= 2 {
			switch segments[0] {
			case "embed", "shorts", "live", "v":
				if youTubeVideoIdPattern.MatchString(segments[1]) {
					return segments[1], nil
				}
			}
		}
	}

	return "", errors.New("could not find a YouTube video ID in that value")
}

func firstPathSegment(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) == 0 {
		return ""
	}
	return segments[0]
}
