package guess

import (
	"context"
	"strings"
	"unicode"
)

// Normalized is the built-in Judge: it normalizes both sides and checks how
// much of the authored title and artist appear in what the player typed.
//
// It is not clever, and it is not meant to be — it is the implementation that
// makes the game playable with no external dependency, and the fallback that
// keeps the game playable when a cleverer Judge is unavailable. It handles
// case, punctuation, accents, a leading "the", featured-artist clauses and
// remaster suffixes, and tolerates small typos. It does not handle nicknames,
// translations, or "the one from the film" — that is what a model-backed Judge
// is for.
type Normalized struct{}

// tokenMatchThreshold is how similar two words must be to count as the same
// one. 0.8 accepts "herose" for "heroes" while rejecting "hero" for "heroes".
const tokenMatchThreshold = 0.8

// coverageThreshold is the fraction of the authored words that must appear in
// the guess. Below 1.0 so "Sgt Pepper" can match "Sgt. Pepper's Lonely Hearts
// Club Band" without the player typing all six words.
const coverageThreshold = 0.6

// distinctiveTokenLength is how long a word must be before matching it alone
// identifies an artist. Four letters keeps "Bowie" and "Cash" while rejecting
// "Boy", "Sun" and similar filler.
const distinctiveTokenLength = 4

func (Normalized) Judge(_ context.Context, in Input) (Verdict, error) {
	guessTokens := tokenize(in.Guess)
	if len(guessTokens) == 0 {
		return Verdict{}, nil
	}

	return Verdict{
		TitleCorrect:  covered(tokenize(in.Title), guessTokens, false),
		ArtistCorrect: covered(tokenize(in.Artist), guessTokens, true),
	}, nil
}

// covered reports whether enough of want's words appear among have's.
//
// surnameCounts relaxes this for artists: people say "Bowie", not "David
// Bowie", and the last word of a performer's name is almost always the
// identifying one (Bowie, Beatles, Björk, Peppers). Matching it alone is
// therefore enough, provided it is a distinctive word rather than something
// like "The" or "Boy". Titles get no such shortcut — the last word of a title
// carries no special weight, and "Together" should not stand in for "Come
// Together".
func covered(want []string, have []string, surnameCounts bool) bool {
	if len(want) == 0 {
		return false
	}

	matched := 0
	for _, wantToken := range want {
		for _, haveToken := range have {
			if similar(wantToken, haveToken) {
				matched++
				break
			}
		}
	}

	if float64(matched)/float64(len(want)) >= coverageThreshold {
		return true
	}

	if surnameCounts {
		last := want[len(want)-1]
		if len(last) >= distinctiveTokenLength {
			for _, haveToken := range have {
				if similar(last, haveToken) {
					return true
				}
			}
		}
	}

	return false
}

// similar reports whether two words are close enough to be the same word.
func similar(a, b string) bool {
	if a == b {
		return true
	}
	// Very short words are matched exactly: at two or three letters, one edit
	// is most of the word, and "in"/"it"/"is" would all collapse together.
	if len(a) <= 3 || len(b) <= 3 {
		return false
	}

	distance := editDistance(a, b)
	longest := len(a)
	if len(b) > longest {
		longest = len(b)
	}
	return 1.0-float64(distance)/float64(longest) >= tokenMatchThreshold
}

// tokenize normalizes a string into comparable words: accents folded, bracketed
// asides and featured-artist clauses dropped, punctuation removed, a leading
// "the" discarded, and noise words filtered out.
func tokenize(s string) []string {
	s = strings.ToLower(s)
	s = stripBracketed(s)
	s = stripFeaturing(s)
	s = foldAccents(s)

	var builder strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
		default:
			// Everything else, including apostrophes and hyphens, becomes a
			// separator: "rock'n'roll" and "rock n roll" should tokenize alike.
			builder.WriteRune(' ')
		}
	}

	fields := strings.Fields(builder.String())
	tokens := make([]string, 0, len(fields))
	for i, field := range fields {
		if i == 0 && field == "the" && len(fields) > 1 {
			continue
		}
		if isNoiseWord(field) {
			continue
		}
		tokens = append(tokens, field)
	}

	// A title that is nothing but noise words ("The The") still needs
	// something to match against.
	if len(tokens) == 0 {
		return fields
	}
	return tokens
}

// noiseWords are words too common to carry any signal about whether the player
// knows the song. "by" matters especially: players type "Heroes by Bowie".
var noiseWords = map[string]bool{
	"a": true, "an": true, "and": true, "by": true, "for": true,
	"in": true, "of": true, "on": true, "or": true, "to": true,
}

func isNoiseWord(s string) bool {
	return noiseWords[s]
}

// stripBracketed removes parenthesized and bracketed asides, which in practice
// are release furniture rather than the song's name: "(Remastered 2011)",
// "[Official Video]", "(Live at Wembley)".
func stripBracketed(s string) string {
	var builder strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				builder.WriteRune(r)
			}
		}
	}
	return builder.String()
}

// stripFeaturing drops a trailing featured-artist clause. A player naming the
// lead artist has named the song's artist, and requiring the full credit would
// fail guesses that are plainly right.
func stripFeaturing(s string) string {
	for _, marker := range []string{" feat ", " feat. ", " featuring ", " ft ", " ft. ", " with "} {
		if idx := strings.Index(s, marker); idx >= 0 {
			s = s[:idx]
		}
	}
	// Also handle a separator-style credit: "Artist - Live", "Title - Remaster".
	if idx := strings.Index(s, " - "); idx >= 0 {
		s = s[:idx]
	}
	return s
}

// foldAccents maps common accented Latin letters to their base form so
// "Bjork" matches "Björk" and "Beyonce" matches "Beyoncé".
func foldAccents(s string) string {
	var builder strings.Builder
	for _, r := range s {
		if folded, ok := accentFolding[r]; ok {
			builder.WriteString(folded)
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

var accentFolding = map[rune]string{
	'á': "a", 'à': "a", 'â': "a", 'ä': "a", 'ã': "a", 'å': "a", 'ā': "a",
	'é': "e", 'è': "e", 'ê': "e", 'ë': "e", 'ē': "e",
	'í': "i", 'ì': "i", 'î': "i", 'ï': "i", 'ī': "i",
	'ó': "o", 'ò': "o", 'ô': "o", 'ö': "o", 'õ': "o", 'ø': "o", 'ō': "o",
	'ú': "u", 'ù': "u", 'û': "u", 'ü': "u", 'ū': "u",
	'ñ': "n", 'ç': "c", 'ý': "y", 'ÿ': "y",
	'æ': "ae", 'œ': "oe", 'ß': "ss", 'ð': "d", 'þ': "th",
}

// editDistance is optimal string alignment distance: Levenshtein plus
// transposition of two adjacent characters as a single edit.
//
// Plain Levenshtein charges two edits for a transposition, which is the wrong
// price for by far the commonest typing mistake — "herose" for "heroes" would
// score 0.67 similarity and be rejected, while genuinely different words score
// about the same. Counting it as one edit puts it at 0.83 and lets it through.
func editDistance(a, b string) int {
	ra := []rune(a)
	rb := []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}

	// Full matrix rather than a single row: the transposition case needs the
	// row two back, which the rolling-row form has already discarded.
	distance := make([][]int, len(ra)+1)
	for i := range distance {
		distance[i] = make([]int, len(rb)+1)
		distance[i][0] = i
	}
	for j := 0; j <= len(rb); j++ {
		distance[0][j] = j
	}

	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			distance[i][j] = min3(
				distance[i-1][j]+1,
				distance[i][j-1]+1,
				distance[i-1][j-1]+cost,
			)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				if swapped := distance[i-2][j-2] + 1; swapped < distance[i][j] {
					distance[i][j] = swapped
				}
			}
		}
	}

	return distance[len(ra)][len(rb)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
