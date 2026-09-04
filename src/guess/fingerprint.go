package guess

import "strings"

// CanonicalKey is a stable title+artist fingerprint for duplicate detection.
// It reuses the same tokenization as Normalized judging (case, accents,
// punctuation, bracketed asides, featuring clauses, leading "the"), then
// joins tokens so "Heroes" / "David Bowie" and "heroes (Remastered)" /
// "David Bowie" collide, while unrelated songs stay apart.
func CanonicalKey(title, artist string) string {
	titleTokens := tokenize(title)
	artistTokens := tokenize(artist)
	if len(titleTokens) == 0 && len(artistTokens) == 0 {
		return ""
	}
	parts := make([]string, 0, len(titleTokens)+len(artistTokens)+1)
	parts = append(parts, titleTokens...)
	parts = append(parts, "|")
	parts = append(parts, artistTokens...)
	return strings.Join(parts, " ")
}
