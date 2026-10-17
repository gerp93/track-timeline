package database

import (
	"fmt"
	"math"
)

// SmallSample is the denominator below which a rate is shown dimmed and
// flagged: with so few tries the percentage says more about luck than skill.
const SmallSample = 10

// Rate is "Hit out of Total" — the one shape every accuracy figure on the stats
// pages takes. Keeping the counts (not just a percentage) is what lets a page
// always show the sample size next to the number, and lets a tooltip give an
// honest uncertainty range instead of a falsely precise percentage.
type Rate struct {
	Hit   int
	Total int
}

// Has reports whether there is anything to show (Total > 0).
func (r Rate) Has() bool { return r.Total > 0 }

// Small reports whether the sample is too small to lean on.
func (r Rate) Small() bool { return r.Total > 0 && r.Total < SmallSample }

// Misses is Total - Hit.
func (r Rate) Misses() int { return r.Total - r.Hit }

// Pct is the hit rate as a 0-100 number; 0 when there is no sample.
func (r Rate) Pct() float64 {
	if r.Total == 0 {
		return 0
	}
	return 100 * float64(r.Hit) / float64(r.Total)
}

// Percent is the rate for display: "74%", or an en dash when there is no sample.
func (r Rate) Percent() string {
	if r.Total == 0 {
		return "–"
	}
	return fmt.Sprintf("%.0f%%", r.Pct())
}

// Fraction is "124 / 163", or empty when there is no sample.
func (r Rate) Fraction() string {
	if r.Total == 0 {
		return ""
	}
	return fmt.Sprintf("%d / %d", r.Hit, r.Total)
}

// Width is the bar fill, 0-100.
func (r Rate) Width() int { return int(math.Round(r.Pct())) }

// Heat is the heat-map intensity, 0-100, for tables that colour a cell by rate.
func (r Rate) Heat() int { return r.Width() }

// Interval is the 95% Wilson score interval for the true rate, as 0-100
// percentages. Wilson (not the textbook normal approximation) because the
// samples here are tiny and often sit near 0% or 100%, where the normal
// interval collapses to a point or spills past the ends.
func (r Rate) Interval() (float64, float64) {
	if r.Total == 0 {
		return 0, 100
	}
	const z = 1.96
	n := float64(r.Total)
	p := float64(r.Hit) / n
	denom := 1 + z*z/n
	centre := (p + z*z/(2*n)) / denom
	margin := z * math.Sqrt(p*(1-p)/n+z*z/(4*n*n)) / denom
	return 100 * math.Max(0, centre-margin), 100 * math.Min(1, centre+margin)
}

// Tip is the tooltip text: the counts plus the plausible range.
func (r Rate) Tip() string {
	if r.Total == 0 {
		return "No data yet."
	}
	lo, hi := r.Interval()
	tip := fmt.Sprintf("%d of %d · likely range %.0f–%.0f%%", r.Hit, r.Total, lo, hi)
	if r.Small() {
		tip += " · small sample"
	}
	return tip
}

// Smoothed pulls a rate toward a prior as if the prior had been observed
// `strength` extra times. It is how songs are ranked: a song missed once in one
// try has a raw rate of 0%, but is not truly "the hardest song" — shrinking
// toward the table-wide average lets a song earn an extreme position only with
// evidence. Returns a 0-1 value.
func (r Rate) Smoothed(prior float64, strength float64) float64 {
	return (float64(r.Hit) + prior*strength) / (float64(r.Total) + strength)
}

// add returns the sum of two rates.
func (r Rate) add(o Rate) Rate { return Rate{Hit: r.Hit + o.Hit, Total: r.Total + o.Total} }

// tally records one outcome.
func (r *Rate) tally(hit bool) {
	r.Total++
	if hit {
		r.Hit++
	}
}

// minSampleForDelta is the smallest sample worth comparing against a baseline.
const minSampleForDelta = 5

// Delta is how many percentage points r sits above (positive) or below the
// baseline rate.
func (r Rate) Delta(base Rate) float64 { return r.Pct() - base.Pct() }

// DeltaLabel is Delta for display ("+5", "-3"), blank when either side has too
// little data to compare.
func (r Rate) DeltaLabel(base Rate) string {
	if r.Total < minSampleForDelta || base.Total == 0 {
		return ""
	}
	d := r.Delta(base)
	if d > -0.5 && d < 0.5 {
		return "±0"
	}
	return fmt.Sprintf("%+.0f", d)
}

// DeltaClass colours a delta: only a gap of three points or more is worth
// calling better or worse.
func (r Rate) DeltaClass(base Rate) string {
	if r.Total < minSampleForDelta || base.Total == 0 {
		return ""
	}
	switch d := r.Delta(base); {
	case d >= 3:
		return "stat-pos"
	case d <= -3:
		return "stat-neg"
	}
	return ""
}
