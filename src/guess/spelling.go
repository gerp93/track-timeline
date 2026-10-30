package guess

import "strings"

// Misspelled reports whether typed names answer but gets its spelling wrong: it
// is not the answer once both are normalized (case, accents, punctuation, "the",
// bracketed asides and filler words ignored), yet close enough to it that a
// handful of slipped letters explains the difference. "herose" for "Heroes" and
// "thamas rhet" for "Thomas Rhett" are; "bowie heroes" for "Heroes" is not
// (extra words, not typos) and neither is a different name altogether.
//
// This does not decide whether a guess is right. The judge does that, and may
// forgive a lot more than this notices; Misspelled only picks out the guesses
// the judge forgave for a spelling slip, so they can be counted.
func Misspelled(typed string, answer string) bool {
	got := strings.Join(tokenize(typed), " ")
	want := strings.Join(tokenize(answer), " ")
	if got == "" || want == "" || got == want {
		return false
	}

	distance := editDistance(got, want)
	allowed := len([]rune(want)) / 3
	return distance >= 1 && distance <= max(allowed, 1)
}
