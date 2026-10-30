package guess

import "testing"

func TestMisspelled(t *testing.T) {
	cases := []struct {
		name   string
		typed  string
		answer string
		want   bool
	}{
		{"exact", "Heroes", "Heroes", false},
		{"case and punctuation only", "heroes!", "Heroes", false},
		{"transposed letters", "herose", "Heroes", true},
		{"two slips in a name", "thamas rhet", "Thomas Rhett", true},
		{"dropped apostrophe and a letter", "gansta paradise", "Gangsta's Paradise", true},
		{"accent folded is not a slip", "beyonce", "Beyoncé", false},
		{"leading the is not a slip", "beatles", "The Beatles", false},
		{"filler words are not a slip", "heroes by david bowie", "Heroes by David Bowie", false},
		{"extra words are not typos", "bowie heroes", "Heroes", false},
		{"a different word", "yesterday", "Heroes", false},
		{"empty guess", "", "Heroes", false},
		{"a short word has no room for a slip", "ax", "Ace", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Misspelled(c.typed, c.answer); got != c.want {
				t.Errorf("Misspelled(%q, %q) = %v, want %v", c.typed, c.answer, got, c.want)
			}
		})
	}
}
