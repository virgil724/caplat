package capture

import "testing"

func TestClockMonotonic(t *testing.T) {
	freq := ClockFreq()
	if freq <= 0 {
		t.Fatalf("ClockFreq() = %d, want > 0", freq)
	}
	t1 := Now()
	t2 := Now()
	if t1 <= 0 {
		t.Fatalf("Now() = %d, want > 0", t1)
	}
	if t2 < t1 {
		t.Fatalf("clock moved backwards: %d -> %d", t1, t2)
	}
}
