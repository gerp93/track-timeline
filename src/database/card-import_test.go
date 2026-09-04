package database

import (
	"database/sql"
	"strings"
	"testing"
)

func TestParseCardImportJSONMalformedVideoClearsId(t *testing.T) {
	data := []byte(`[
		{
			"title": "Cinnamon Girl",
			"artist": "Neil Young",
			"year": 1969,
			"videoId": "https://youtu.be/a1y3eX_aM123",
			"category": "Rock"
		},
		{
			"title": "Heroes",
			"artist": "David Bowie",
			"videoId": "dQw4w9wgXcQ",
			"category": "Rock"
		}
	]`)

	entries, err := ParseCardImportJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries", len(entries))
	}
	if !entries[0].MissingVideo || entries[0].VideoId != "" {
		t.Fatalf("malformed video should clear id, got %+v", entries[0])
	}
	if entries[1].MissingVideo || entries[1].VideoId != "dQw4w9wgXcQ" {
		t.Fatalf("valid video should parse, got %+v", entries[1])
	}
}

func TestParseCardImportJSONBlankVideo(t *testing.T) {
	data := []byte(`[{"title":"A","artist":"B","videoId":""}]`)
	entries, err := ParseCardImportJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if !entries[0].MissingVideo || entries[0].VideoId != "" {
		t.Fatalf("blank video should be missing, got %+v", entries[0])
	}
}

func TestFormatImportReport(t *testing.T) {
	got := (CardImportResult{
		Imported:                 3,
		Failed:                   1,
		SkippedDuplicateVideo:    2,
		SkippedDuplicateMetadata: 4,
		MissingVideo:             1,
	}).FormatImportReport()
	for _, want := range []string{
		"Imported: 3",
		"Failed: 1",
		"Skipped (duplicate video ID): 2",
		"Skipped (duplicate title/artist/year): 4",
		"Imported with no video link (Dead Videos): 1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("report missing %q in %q", want, got)
		}
	}
}

func TestImportMetadataKey(t *testing.T) {
	withYear := importMetadataKey("Heroes", "David Bowie", sql.NullInt64{Int64: 1977, Valid: true})
	noYear := importMetadataKey("Heroes", "David Bowie", sql.NullInt64{})
	if withYear == noYear {
		t.Fatal("null year and 1977 should be different keys")
	}
}
