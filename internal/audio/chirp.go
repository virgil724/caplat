// Package audio measures audio latency: it plays a short chirp every
// period on the output feeding the capture card and finds each one in the
// captured audio by normalized cross-correlation, which is exact to a sample
// and indifferent to volume, filtering and lossy codecs.
package audio

import "math"

// Chirp returns a linear sweep from f0 to f1 Hz lasting n samples at rate,
// shaped by a Hann window so it starts and ends at zero.
func Chirp(rate, n int, f0, f1 float64) []float64 {
	out := make([]float64, n)
	dur := float64(n) / float64(rate)
	k := (f1 - f0) / dur
	for i := range out {
		t := float64(i) / float64(rate)
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		out[i] = w * math.Sin(2*math.Pi*(f0*t+k*t*t/2))
	}
	return out
}

// Detector finds a template in a stream of samples. Push samples in order;
// OnHit gets the index (from the first sample pushed) where each occurrence
// starts.
type Detector struct {
	tmpl     []float64
	tmplNorm float64

	// Threshold is the normalized correlation (0..1) a hit needs; MinRMS
	// the window's RMS, so silence never matches.
	Threshold float64
	MinRMS    float64
	// Refractory is the minimum number of samples between hits.
	Refractory int64
	OnHit      func(start int64, score float64)

	hist     []float64 // the last len(tmpl)-1 samples, then the new ones
	consumed int64     // index of hist[0]
	cand     int64
	candS    float64
	lastHit  int64
}

// NewDetector returns a detector for tmpl.
func NewDetector(tmpl []float64) *Detector {
	var e float64
	for _, v := range tmpl {
		e += v * v
	}
	return &Detector{tmpl: tmpl, tmplNorm: math.Sqrt(e), Threshold: 0.5, MinRMS: 1e-3,
		Refractory: int64(len(tmpl)) * 4, cand: -1, lastHit: math.MinInt64 / 2}
}

// Push adds samples.
func (d *Detector) Push(x []float64) {
	L := len(d.tmpl)
	d.hist = append(d.hist, x...)
	if len(d.hist) < L {
		return
	}
	// Energy of the first window, then rolled.
	var e float64
	for _, v := range d.hist[:L] {
		e += v * v
	}
	last := len(d.hist) - L
	for s := 0; s <= last; s++ {
		if s > 0 {
			o, n := d.hist[s-1], d.hist[s+L-1]
			e += n*n - o*o
			if e < 0 {
				e = 0
			}
		}
		start := d.consumed + int64(s)
		if d.cand >= 0 && start > d.cand+int64(L)/2 {
			if d.cand-d.lastHit >= d.Refractory {
				d.lastHit = d.cand
				if d.OnHit != nil {
					d.OnHit(d.cand, d.candS)
				}
			}
			d.cand = -1
		}
		if e/float64(L) < d.MinRMS*d.MinRMS {
			continue
		}
		w := d.hist[s : s+L]
		var dot float64
		for i, t := range d.tmpl {
			dot += w[i] * t
		}
		score := dot / (math.Sqrt(e) * d.tmplNorm)
		if score >= d.Threshold && (d.cand < 0 || score > d.candS) {
			d.cand, d.candS = start, score
		}
	}
	// Keep the last L-1 samples for the next windows.
	keep := L - 1
	drop := len(d.hist) - keep
	d.consumed += int64(drop)
	d.hist = append(d.hist[:0], d.hist[drop:]...)
}
