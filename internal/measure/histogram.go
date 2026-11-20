// Package measure pairs vblank times with capture arrival times and keeps
// latency statistics in fixed memory, so a run can last as long as needed.
package measure

import "math"

const (
	// BinMS is the histogram resolution.
	BinMS = 0.1
	// RangeMS is the top of the histogram; larger samples go to the overflow
	// bin (and still count toward Max).
	RangeMS = 1000.0
	nBins   = int(RangeMS / BinMS)
)

// Histogram is a fixed-resolution latency histogram (0.1 ms bins, 0-1000 ms).
// Quantiles are exact to the bin width. The zero value is empty and ready.
type Histogram struct {
	Bins     [nBins]uint64
	Overflow uint64
	N        uint64
	Sum      float64
	MinMS    float64
	MaxMS    float64
}

// Add records one latency sample in milliseconds. Negative values (clock or
// matching errors) are clamped into the first bin.
func (h *Histogram) Add(ms float64) {
	if h.N == 0 || ms < h.MinMS {
		h.MinMS = ms
	}
	if h.N == 0 || ms > h.MaxMS {
		h.MaxMS = ms
	}
	h.N++
	h.Sum += ms
	if ms >= RangeMS {
		h.Overflow++
		return
	}
	i := int(ms / BinMS)
	if i < 0 {
		i = 0
	}
	h.Bins[i]++
}

// Merge adds o into h.
func (h *Histogram) Merge(o *Histogram) {
	if o.N == 0 {
		return
	}
	if h.N == 0 || o.MinMS < h.MinMS {
		h.MinMS = o.MinMS
	}
	if h.N == 0 || o.MaxMS > h.MaxMS {
		h.MaxMS = o.MaxMS
	}
	for i := range h.Bins {
		h.Bins[i] += o.Bins[i]
	}
	h.Overflow += o.Overflow
	h.N += o.N
	h.Sum += o.Sum
}

// Reset empties h.
func (h *Histogram) Reset() { *h = Histogram{} }

// Quantile returns the q-quantile (0..1) as the middle of its bin, clamped to
// the observed min and max. It returns NaN when empty.
func (h *Histogram) Quantile(q float64) float64 {
	if h.N == 0 {
		return math.NaN()
	}
	rank := uint64(math.Ceil(q * float64(h.N)))
	if rank < 1 {
		rank = 1
	}
	var cum uint64
	for i, c := range h.Bins {
		cum += c
		if cum >= rank {
			v := (float64(i) + 0.5) * BinMS
			return math.Min(math.Max(v, h.MinMS), h.MaxMS)
		}
	}
	return h.MaxMS // in the overflow bin
}

// Stats summarises a histogram.
type Stats struct {
	N                            uint64
	Min, P10, P50, P90, P99, Max float64
	Mean                         float64
}

// Stats returns the summary of h.
func (h *Histogram) Stats() Stats {
	s := Stats{N: h.N}
	if h.N == 0 {
		nan := math.NaN()
		s.Min, s.P10, s.P50, s.P90, s.P99, s.Max, s.Mean = nan, nan, nan, nan, nan, nan, nan
		return s
	}
	s.Min, s.Max = h.MinMS, h.MaxMS
	s.P10, s.P50, s.P90, s.P99 = h.Quantile(0.10), h.Quantile(0.50), h.Quantile(0.90), h.Quantile(0.99)
	s.Mean = h.Sum / float64(h.N)
	return s
}
