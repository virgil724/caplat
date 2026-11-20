package audio

import (
	"math"
	"math/rand"
	"testing"
)

// Chirps buried in noise, quieter, low-pass filtered and pushed in odd
// chunk sizes are found at their exact sample.
func TestDetectorFindsChirps(t *testing.T) {
	const rate = 48000
	tmpl := Chirp(rate, 480, 1000, 8000)
	r := rand.New(rand.NewSource(1))
	sig := make([]float64, rate*3)
	for i := range sig {
		sig[i] = 0.01 * r.NormFloat64()
	}
	want := []int64{1000, 25000, 49000, 73123, 120001}
	for _, at := range want {
		for i, v := range tmpl {
			sig[int(at)+i] += 0.2 * v
		}
	}
	// A gentle one-pole low-pass, like a card's filter.
	var y float64
	for i, v := range sig {
		y += 0.6 * (v - y)
		sig[i] = y
	}
	var got []int64
	d := NewDetector(tmpl)
	d.OnHit = func(start int64, score float64) { got = append(got, start) }
	for i := 0; i < len(sig); {
		n := 1 + r.Intn(700)
		if i+n > len(sig) {
			n = len(sig) - i
		}
		d.Push(sig[i : i+n])
		i += n
	}
	if len(got) != len(want) {
		t.Fatalf("hits %v, want %v", got, want)
	}
	for i := range want {
		// The low-pass delays the signal by about a sample.
		if diff := got[i] - want[i]; diff < 0 || diff > 2 {
			t.Errorf("hit %d at %d, want %d", i, got[i], want[i])
		}
	}
}

func TestSilenceNeverMatches(t *testing.T) {
	d := NewDetector(Chirp(48000, 480, 1000, 8000))
	d.OnHit = func(int64, float64) { t.Fatal("hit in silence") }
	d.Push(make([]float64, 48000))
	tone := make([]float64, 48000)
	for i := range tone {
		tone[i] = 0.3 * math.Sin(2*math.Pi*440*float64(i)/48000)
	}
	d.Push(tone)
}
