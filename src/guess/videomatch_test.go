package guess

import "testing"

func TestCompareToVideo(t *testing.T) {
	cases := []struct {
		name                                string
		title, artist, videoTitle, channel  string
		wantTitleAtLeast, wantArtistAtLeast float64
		wantTitleBelow, wantArtistBelow     float64
	}{
		{
			name:  "artist dash title with video noise",
			title: "Bohemian Rhapsody", artist: "Queen",
			videoTitle: "Queen - Bohemian Rhapsody (Official Video Remastered)", channel: "Queen Official",
			wantTitleAtLeast: 100, wantArtistAtLeast: 100,
		},
		{
			name:  "title only in video, artist from the channel",
			title: "Heroes", artist: "David Bowie",
			videoTitle: "Heroes (2017 Remaster)", channel: "David Bowie - Topic",
			wantTitleAtLeast: 100, wantArtistAtLeast: 100,
		},
		{
			name:  "song title containing with",
			title: "Dancing With Myself", artist: "Billy Idol",
			videoTitle: "Billy Idol - Dancing With Myself", channel: "BillyIdolVEVO",
			wantTitleAtLeast: 100, wantArtistAtLeast: 100,
		},
		{
			name:  "hyphenated artist name",
			title: "Take On Me", artist: "a-ha",
			videoTitle: "a-ha - Take On Me (Official Video) [4K]", channel: "a-ha",
			wantTitleAtLeast: 100, wantArtistAtLeast: 100,
		},
		{
			name:  "completely different song",
			title: "Bohemian Rhapsody", artist: "Queen",
			videoTitle: "Rick Astley - Never Gonna Give You Up", channel: "Rick Astley",
			wantTitleBelow: 20, wantArtistBelow: 20,
		},
		{
			name:  "right artist wrong song",
			title: "Bohemian Rhapsody", artist: "Queen",
			videoTitle: "Queen - Don't Stop Me Now", channel: "Queen Official",
			wantTitleBelow: 20, wantArtistAtLeast: 100,
		},
		{
			name:  "glued uploader name",
			title: "Dancing Queen", artist: "ABBA",
			videoTitle: "Dancing Queen", channel: "ABBAVEVO",
			wantTitleAtLeast: 100, wantArtistAtLeast: 100,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CompareToVideo(c.title, c.artist, c.videoTitle, c.channel)
			if got.TitlePercent < c.wantTitleAtLeast {
				t.Errorf("title percent %.0f, want at least %.0f", got.TitlePercent, c.wantTitleAtLeast)
			}
			if got.ArtistPercent < c.wantArtistAtLeast {
				t.Errorf("artist percent %.0f, want at least %.0f", got.ArtistPercent, c.wantArtistAtLeast)
			}
			if c.wantTitleBelow > 0 && got.TitlePercent >= c.wantTitleBelow {
				t.Errorf("title percent %.0f, want below %.0f", got.TitlePercent, c.wantTitleBelow)
			}
			if c.wantArtistBelow > 0 && got.ArtistPercent >= c.wantArtistBelow {
				t.Errorf("artist percent %.0f, want below %.0f", got.ArtistPercent, c.wantArtistBelow)
			}
		})
	}
}
