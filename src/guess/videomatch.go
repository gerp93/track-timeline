package guess

import (
	"strings"
	"unicode"
)

// VideoMatch scores how well a YouTube video's own title and uploader line up
// with the song a card says it is, so an admin can find links that point at the
// wrong song.
//
// Both numbers are 0-100: the share of the authored words that turn up
// anywhere in what YouTube shows. Order is ignored, because YouTube titles are
// free-form ("Artist - Song", "Song | Artist", "Song (Official Video)") and the
// question here is "is this the same song", not "did a player type it right".
type VideoMatch struct {
	TitlePercent  float64
	ArtistPercent float64
}

// CompareToVideo scores a card's title and artist against the title and channel
// of the video linked to it. The artist may appear in either the video title or
// the channel name ("Queen - Topic", "QueenVEVO"), so both are searched.
func CompareToVideo(title, artist, videoTitle, channel string) VideoMatch {
	videoTokens := looseTokenize(videoTitle)
	return VideoMatch{
		TitlePercent:  presence(tokenize(title), videoTokens),
		ArtistPercent: presence(tokenize(artist), append(videoTokens, looseTokenize(channel)...)),
	}
}

// presence is the average best similarity of each wanted word against the words
// available, as a percentage. A wanted word that is a prefix-compatible
// fragment of a longer available word (a channel called "QueenVEVO" holding
// "queen") still counts, since uploader names glue words together.
func presence(want, have []string) float64 {
	if len(want) == 0 {
		return 0
	}
	sum := 0.0
	for _, w := range want {
		best := 0.0
		for _, h := range have {
			s := tokenSimilarity(w, h)
			if s < tokenMatchThreshold && len(w) >= distinctiveTokenLength && strings.Contains(h, w) {
				s = 1
			}
			if s > best {
				best = s
			}
		}
		if best >= tokenMatchThreshold {
			sum += best
		}
	}
	return 100 * sum / float64(len(want))
}

// looseTokenize is tokenize for text YouTube wrote rather than an admin: it
// keeps everything after a " - " (that is where the song title usually is) and
// after "feat"/"with" (a song can be called "Dancing With Myself"), and only
// drops bracketed asides, accents and punctuation. Words are joined across an
// unspaced hyphen, matching tokenize.
func looseTokenize(s string) []string {
	s = strings.ToLower(s)
	s = stripBracketed(s)
	s = foldAccents(s)

	// A hyphen inside a word joins it ("a-ha" is "aha", as tokenize has it); a
	// hyphen with spaces around it separates artist from title and must not.
	runes := []rune(s)
	joins := func(i int) bool {
		return i > 0 && i < len(runes)-1 &&
			(unicode.IsLetter(runes[i-1]) || unicode.IsDigit(runes[i-1])) &&
			(unicode.IsLetter(runes[i+1]) || unicode.IsDigit(runes[i+1]))
	}

	var builder strings.Builder
	for i, r := range runes {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
		case r == '-' && joins(i):
		default:
			builder.WriteRune(' ')
		}
	}

	fields := strings.Fields(builder.String())
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		if !isNoiseWord(field) {
			tokens = append(tokens, field)
		}
	}
	return tokens
}
