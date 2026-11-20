package measure

import (
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/virgil724/caplat/internal/barcode"
)

func TestHistogramQuantilesMatchSorted(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	var h Histogram
	var xs []float64
	for i := 0; i < 20000; i++ {
		v := 30 + r.NormFloat64()*4
		if i%97 == 0 {
			v += 40 // a second mode
		}
		h.Add(v)
		xs = append(xs, v)
	}
	sort.Float64s(xs)
	for _, q := range []float64{0.1, 0.5, 0.9, 0.99} {
		want := xs[int(math.Ceil(q*float64(len(xs))))-1]
		if got := h.Quantile(q); math.Abs(got-want) > BinMS {
			t.Errorf("q%.2f: got %.3f want %.3f", q, got, want)
		}
	}
	s := h.Stats()
	if s.Min != xs[0] || s.Max != xs[len(xs)-1] || s.N != uint64(len(xs)) {
		t.Errorf("stats %+v", s)
	}
}

func TestHistogramOverflowAndMerge(t *testing.T) {
	var a, b Histogram
	a.Add(10)
	b.Add(2000) // overflow
	a.Merge(&b)
	if a.N != 2 || a.Overflow != 1 || a.MaxMS != 2000 || a.Quantile(1) != 2000 {
		t.Fatalf("%+v", a.Stats())
	}
	var e Histogram
	if !math.IsNaN(e.Quantile(0.5)) {
		t.Fatal("empty quantile not NaN")
	}
}

const freq = 10_000_000 // 10 MHz, like QPC

func TestMatcherPairsInEitherOrder(t *testing.T) {
	var got []Sample
	m := NewMatcher(freq, func(s Sample) { got = append(got, s) })
	m.Vblank(1, 1000)
	m.Captured(1, true, 1000+350_000) // 35 ms later
	m.Captured(2, true, 2000+350_000) // capture first (cannot happen, but must not break)
	m.Vblank(2, 2000)
	m.Captured(2, true, 9_999_999) // a repeated capture of frame 2: ignored
	if len(got) != 2 || got[0].LatencyMS != 35 || got[1].Frame != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestMatcherCountsUnmatched(t *testing.T) {
	m := NewMatcher(freq, nil)
	m.Captured(0, false, 0) // before any barcode: start-up black, not a failure
	m.Vblank(5, 1)
	m.Captured(9, true, 2)
	m.Captured(0, false, 3)
	m.Captured(0, true, 4) // warm-up code: neither matched nor counted
	m.Flush()
	c := m.Counts()
	if c.OnlyVblank != 1 || c.OnlyCapture != 1 || c.DecodeFail != 1 || c.Captured != 4 {
		t.Fatalf("%+v", c)
	}
}

// After the 24-bit code wraps, captures must still map to the right frame.
func TestMatcherUnwrap(t *testing.T) {
	var got []uint64
	m := NewMatcher(freq, func(s Sample) { got = append(got, s.Frame) })
	span := uint64(barcode.Mask) + 1
	// Frames whose low 24 bits are 0 are never presented (code 0 is warm-up).
	for _, f := range []uint64{span - 2, span - 1, span + 1, 3*span + 5} {
		m.Vblank(f, int64(f))
		m.Captured(uint32(f&uint64(barcode.Mask)), true, int64(f)+10)
	}
	want := []uint64{span - 2, span - 1, span + 1, 3*span + 5}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

// Old entries are dropped so memory stays bounded on long runs.
func TestMatcherExpires(t *testing.T) {
	m := NewMatcher(freq, nil)
	for i := uint64(1); i <= 100_000; i++ {
		m.Vblank(i, int64(i)*166_667) // 60 Hz, no captures at all
	}
	if n := len(m.pend); n > 60*7 {
		t.Fatalf("%d pending entries kept", n)
	}
	if m.Counts().OnlyVblank == 0 {
		t.Fatal("expired frames not counted")
	}
}

func TestMatcherRejectsNegative(t *testing.T) {
	var got []Sample
	m := NewMatcher(freq, func(s Sample) { got = append(got, s) })
	m.Captured(7, true, 1000)
	m.Vblank(7, 5000) // shown after it arrived: confusion, not a sample
	if len(got) != 0 || m.Counts().Mismatched != 1 {
		t.Fatalf("got %v counts %+v", got, m.Counts())
	}
}
