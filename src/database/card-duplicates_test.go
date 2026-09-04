package database

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBuildDuplicateGroupsRespectsDismissals(t *testing.T) {
	id1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	id2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	id3 := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	cards := []DuplicateCard{
		{CardId: id1, Title: "Heroes", Artist: "David Bowie", DeckName: "A"},
		{CardId: id2, Title: "heroes (Remastered)", Artist: "David Bowie", DeckName: "B"},
		{CardId: id3, Title: "Space Oddity", Artist: "David Bowie", DeckName: "A"},
	}

	groups := BuildDuplicateGroups(cards, nil, "")
	if len(groups) != 1 || len(groups[0].Cards) != 2 {
		t.Fatalf("want one Heroes group of 2, got %+v", groups)
	}

	dismissed := map[[2]uuid.UUID]bool{
		orderedPairKey(id1, id2): true,
	}
	groups = BuildDuplicateGroups(cards, dismissed, "")
	if len(groups) != 0 {
		t.Fatalf("dismissed pair should hide group, got %+v", groups)
	}

	restored := BuildDismissedDuplicateGroups(cards, dismissed, "")
	if len(restored) != 1 || len(restored[0].Cards) != 2 {
		t.Fatalf("want one dismissed Heroes group of 2, got %+v", restored)
	}
}

func TestExactDuplicateLatestIdsKeepsOldest(t *testing.T) {
	id1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	id2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	id3 := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	older := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	cards := []DuplicateCard{
		{CardId: id1, Title: "Heroes", Artist: "David Bowie", ReleaseYear: 1977, YouTubeVideoId: "abcdefghijk", CreatedOnDate: older, DeckName: "A"},
		{CardId: id2, Title: "Heroes", Artist: "David Bowie", ReleaseYear: 1977, YouTubeVideoId: "abcdefghijk", CreatedOnDate: newer, DeckName: "B"},
		{CardId: id3, Title: "Heroes", Artist: "David Bowie", ReleaseYear: 1977, YouTubeVideoId: "different001", CreatedOnDate: newer, DeckName: "A"},
	}

	got := ExactDuplicateLatestIds(cards)
	if len(got) != 1 || got[0] != id2 {
		t.Fatalf("want only newer exact copy %v deleted, got %v", id2, got)
	}
}

func TestExactTitleArtistLatestIdsIgnoresVideoRequiresYear(t *testing.T) {
	id1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	id2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	id3 := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	older := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	cards := []DuplicateCard{
		{CardId: id1, Title: "Heroes", Artist: "David Bowie", ReleaseYear: 1977, YouTubeVideoId: "aaaaaaaaaaa", CreatedOnDate: older},
		{CardId: id2, Title: "Heroes", Artist: "David Bowie", ReleaseYear: 1977, YouTubeVideoId: "bbbbbbbbbbb", CreatedOnDate: newer},
		{CardId: id3, Title: "Heroes", Artist: "David Bowie", ReleaseYear: 1978, YouTubeVideoId: "ccccccccccc", CreatedOnDate: newer},
	}
	got := ExactTitleArtistLatestIds(cards)
	if len(got) != 1 || got[0] != id2 {
		t.Fatalf("want only same-year newer copy deleted, got %v", got)
	}
}
