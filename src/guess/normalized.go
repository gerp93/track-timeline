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

// distinctiveTokenLength is how long a word must be before matching it alone
// identifies an artist. Four letters keeps "Bowie" and "Cash" while rejecting
// "Boy", "Sun" and similar filler.
const distinctiveTokenLength = 4

func (Normalized) Judge(_ context.Context, in Input) (Verdict, error) {
	titleHave := tokenize(in.TitleGuess)
	artistHave := tokenize(in.ArtistGuess)
	if len(titleHave) == 0 && len(artistHave) == 0 {
		combined := tokenize(in.Guess)
		titleHave = combined
		artistHave = combined
	}

	if len(titleHave) == 0 && len(artistHave) == 0 {
		return Verdict{}, nil
	}

	titleCorrect, titlePercent := covered(tokenize(in.Title), titleHave, false, in.MinMatchPercent)
	artistCorrect, artistPercent := covered(tokenize(in.Artist), artistHave, true, in.MinMatchPercent)

	return Verdict{
		TitleCorrect:       titleCorrect,
		ArtistCorrect:      artistCorrect,
		TitleMatchPercent:  titlePercent,
		ArtistMatchPercent: artistPercent,
	}, nil
}

// covered reports whether enough of want's words appear among have's, plus how
// much (0-100) for display.
//
// surnameCounts relaxes the boolean for artists: people say "Bowie", not
// "David Bowie", and the last word of a performer's name is almost always the
// identifying one (Bowie, Beatles, Björk, Peppers). Matching it alone is
// therefore enough, provided it is a distinctive word rather than something
// like "The" or "Boy". Titles get no such shortcut — the last word of a title
// carries no special weight, and "Together" should not stand in for "Come
// Together". The surname shortcut counts as a full artist match (100%),
// because the last name is treated as identifying the performer.
func covered(want []string, have []string, surnameCounts bool, minMatchPercent int) (bool, float64) {
	if len(want) == 0 {
		return false, 0
	}
	if minMatchPercent == 0 {
		minMatchPercent = 60
	}

	matchedWords, similaritySum := orderedCoverage(want, have)
	percent := 100 * similaritySum / float64(len(want))
	if percent > 100 {
		percent = 100
	}

	allPresent := unorderedAllPresent(want, have)
	inOrder := matchedWords == len(want)
	if allPresent && !inOrder {
		return false, percent
	}

	if meetsMatchBar(100*float64(matchedWords)/float64(len(want)), minMatchPercent) {
		return true, percent
	}

	if surnameCounts {
		last := want[len(want)-1]
		if len(last) >= distinctiveTokenLength {
			for _, haveToken := range have {
				if similar(last, haveToken) {
					return true, 100
				}
			}
		}
	}

	return false, percent
}

// orderedCoverage walks authored words left to right and consumes a matching
// guess word at or after the previous hit, so "take me on" does not fully
// cover "take on me".
func orderedCoverage(want []string, have []string) (int, float64) {
	matched := 0
	simSum := 0.0
	start := 0
	for _, wantToken := range want {
		consumed, best := matchWantFrom(have, start, wantToken)
		if consumed > 0 {
			matched++
			simSum += best
			start += consumed
		}
	}
	return matched, simSum
}

// matchWantFrom finds wantToken in have[start:], either as one word or as
// consecutive guess words stuck together ("fire"+"work" = "firework").
func matchWantFrom(have []string, start int, wantToken string) (consumed int, best float64) {
	for i := start; i < len(have); i++ {
		concat := ""
		for j := i; j < len(have); j++ {
			concat += have[j]
			s := tokenSimilarity(wantToken, concat)
			if s >= tokenMatchThreshold && s > best {
				best = s
				consumed = j - start + 1
				if s == 1 {
					return consumed, best
				}
			}
			if len(concat) > len(wantToken)+2 {
				break
			}
		}
		if consumed > 0 {
			return consumed, best
		}
	}
	return 0, 0
}

func unorderedAllPresent(want []string, have []string) bool {
	used := make([]bool, len(have))
	for _, wantToken := range want {
		hit := false
		for i, haveToken := range have {
			if used[i] {
				continue
			}
			if similar(wantToken, haveToken) {
				used[i] = true
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// similar reports whether two words are close enough to be the same word.
func similar(a, b string) bool {
	return tokenSimilarity(a, b) >= tokenMatchThreshold
}

// tokenSimilarity is 1 for an exact match, then 1 minus edit-distance over
// the longer word. Short words (3 letters or fewer) are all-or-nothing so
// "in"/"it"/"is" do not collapse together. Used for the displayed match
// percent so a typo that still clears the boolean bar does not report 100%.
func tokenSimilarity(a, b string) float64 {
	if a == b {
		return 1
	}
	if len(a) <= 3 || len(b) <= 3 {
		return 0
	}

	distance := editDistance(a, b)
	longest := len(a)
	if len(b) > longest {
		longest = len(b)
	}
	if longest == 0 {
		return 1
	}
	s := 1.0 - float64(distance)/float64(longest)
	if s < 0 {
		return 0
	}
	return s
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
		case r == '-':
			// Hyphens join: "a-ha" and "aha" are the same artist. Turning
			// them into spaces left "a" (noise) + "ha", so "aha" scored 0.
		default:
			// Apostrophes and other punctuation become separators so
			// "rock'n'roll" and "rock n roll" tokenize alike.
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
	"in": true, "of": true, "or": true, "to": true,
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
