package guess

import "testing"

func TestCanonicalKeyCollapsesRemastersAndCase(t *testing.T) {
	a := CanonicalKey("Heroes (Remastered 2011)", "David Bowie")
	b := CanonicalKey("heroes", "DAVID BOWIE")
	if a == "" || a != b {
		t.Fatalf("CanonicalKey mismatch: %q vs %q", a, b)
	}
}

func TestCanonicalKeySeparatesDifferentSongs(t *testing.T) {
	a := CanonicalKey("Heroes", "David Bowie")
	b := CanonicalKey("Space Oddity", "David Bowie")
	if a == b {
		t.Fatalf("expected different keys, both %q", a)
	}
}
