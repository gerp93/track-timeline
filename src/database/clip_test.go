package database

import "testing"

// ResolveClipWindow is random for the sample mode, so these assert the
// invariants that must hold for every roll rather than one exact answer.
func TestResolveClipWindow(t *testing.T) {
	t.Run("full mode plays the whole song", func(t *testing.T) {
		got := ResolveClipWindow(PlaybackFull, 20, 240)
		if got != (ClipWindow{}) {
			t.Errorf("expected the zero window (start at 0, no end), got %+v", got)
		}
	})

	t.Run("intro mode starts at the top", func(t *testing.T) {
		got := ResolveClipWindow(PlaybackIntro, 20, 240)
		want := ClipWindow{StartSeconds: 0, EndSeconds: 20}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("sample stays inside the song and past the lead-in", func(t *testing.T) {
		const duration, clip = 240, 20
		for i := 0; i < 200; i++ {
			got := ResolveClipWindow(PlaybackSample, clip, duration)
			if got.StartSeconds < SampleLeadInSeconds {
				t.Fatalf("start %d is inside the first %ds of the song", got.StartSeconds, SampleLeadInSeconds)
			}
			if got.EndSeconds > duration {
				t.Fatalf("end %d runs past the end of a %ds song", got.EndSeconds, duration)
			}
			if got.EndSeconds-got.StartSeconds != clip {
				t.Fatalf("clip is %ds, want %ds", got.EndSeconds-got.StartSeconds, clip)
			}
		}
	})

	t.Run("sample varies rather than always picking the same window", func(t *testing.T) {
		seen := map[int]bool{}
		for i := 0; i < 200; i++ {
			seen[ResolveClipWindow(PlaybackSample, 20, 600).StartSeconds] = true
		}
		if len(seen) < 2 {
			t.Errorf("expected a range of start points across 200 rolls, got %d distinct", len(seen))
		}
	})

	// Falling back to the intro is what keeps a very short song from
	// producing a window that runs off the end into silence. Unknown
	// duration still gets a random mid-song start rather than always 0.
	t.Run("falls back to the intro when a sample will not fit", func(t *testing.T) {
		want := ClipWindow{StartSeconds: 0, EndSeconds: 20}

		// 45s song: the 30s lead-in leaves only 15s, less than the 20s clip.
		if got := ResolveClipWindow(PlaybackSample, 20, 45); got != want {
			t.Errorf("song too short: got %+v, want %+v", got, want)
		}
		// Exactly at the boundary — a start of 30 would end at 50, which is
		// the whole song, leaving no room to be a middle sample.
		if got := ResolveClipWindow(PlaybackSample, 20, 50); got != want {
			t.Errorf("song exactly clip+lead-in: got %+v, want %+v", got, want)
		}
	})

	t.Run("unknown duration still picks a mid-song sample", func(t *testing.T) {
		seen := map[int]bool{}
		for i := 0; i < 100; i++ {
			got := ResolveClipWindow(PlaybackSample, 20, 0)
			if got.StartSeconds < SampleLeadInSeconds {
				t.Fatalf("start %d is inside the lead-in", got.StartSeconds)
			}
			if got.EndSeconds-got.StartSeconds != 20 {
				t.Fatalf("clip is %ds, want 20", got.EndSeconds-got.StartSeconds)
			}
			seen[got.StartSeconds] = true
		}
		if len(seen) < 2 {
			t.Errorf("expected varied starts with unknown duration, got %d distinct", len(seen))
		}
	})
}

// A re-roll has to be genuinely different audio: never the same start, and
// non-overlapping whenever the song has room for that.
func TestPickDifferentClipWindow(t *testing.T) {
	const clip = 30

	t.Run("never repeats the current start and stays inside the song", func(t *testing.T) {
		current := ClipWindow{StartSeconds: 60, EndSeconds: 90}
		for i := 0; i < 500; i++ {
			got, ok := PickDifferentClipWindow(clip, 240, current)
			if !ok {
				t.Fatal("a 240s song has other clips to offer")
			}
			if got.StartSeconds == current.StartSeconds {
				t.Fatalf("picked the same start again: %+v", got)
			}
			if got.StartSeconds < SampleLeadInSeconds || got.EndSeconds > 240 {
				t.Fatalf("window %+v is outside the usable part of the song", got)
			}
			if got.EndSeconds-got.StartSeconds != clip {
				t.Fatalf("window %+v is not %ds long", got, clip)
			}
		}
	})

	t.Run("avoids overlapping the clip already heard when there is room", func(t *testing.T) {
		current := ClipWindow{StartSeconds: 100, EndSeconds: 130}
		for i := 0; i < 500; i++ {
			got, _ := PickDifferentClipWindow(clip, 300, current)
			if got.StartSeconds < current.EndSeconds && got.EndSeconds > current.StartSeconds {
				t.Fatalf("window %+v overlaps the clip already heard %+v", got, current)
			}
		}
	})

	t.Run("unknown duration still offers another clip", func(t *testing.T) {
		got, ok := PickDifferentClipWindow(clip, 0, ClipWindow{StartSeconds: 50, EndSeconds: 80})
		if !ok || got.StartSeconds == 50 {
			t.Errorf("expected a different window, got %+v ok=%v", got, ok)
		}
	})

	t.Run("a song with no other slice offers nothing, so nothing is charged", func(t *testing.T) {
		// Exactly one clip fits: there is no other start to move to.
		if got, ok := PickDifferentClipWindow(clip, clip, ClipWindow{StartSeconds: 0, EndSeconds: clip}); ok {
			t.Errorf("expected no alternative, got %+v", got)
		}
	})
}
