package guess

import "testing"

func TestMatchGenreReply(t *testing.T) {
	genres := []string{"Pop / Rock", "Hip-Hop", "Country"}

	got, err := matchGenreReply("GENRE=Pop / Rock", genres)
	if err != nil || got != "Pop / Rock" {
		t.Fatalf("exact: got %q %v", got, err)
	}

	got, err = matchGenreReply("genre=hip-hop\nextra", genres)
	if err != nil || got != "Hip-Hop" {
		t.Fatalf("case: got %q %v", got, err)
	}

	if _, err := matchGenreReply("GENRE=Jazz", genres); err == nil {
		t.Fatal("want error for unknown genre")
	}
}
