package barcode

import "testing"

func TestRoundTrip(t *testing.T) {
	for _, n := range []uint32{0, 1, 2, 0x5A5A5A, Mask - 1, Mask} {
		got, ok := FromCells(Encode(n))
		if !ok || got != n {
			t.Fatalf("%#x -> %#x ok=%v", n, got, ok)
		}
	}
}

// Every single-bit error must be rejected, never read as another frame.
func TestSingleBitErrorsRejected(t *testing.T) {
	for _, n := range []uint32{1, 12345, 0xABCDEF} {
		c := Encode(n)
		for i := 0; i < Cells; i++ {
			bad := c
			bad[i] = !bad[i]
			if got, ok := FromCells(bad); ok {
				t.Fatalf("frame %d with cell %d flipped decoded as %d", n, i, got)
			}
		}
	}
}

// render draws the barcode for n on an outW x outH output and scales it to a
// capW x capH luma plane (nearest neighbour), as a capture card would.
func render(n uint32, outW, outH, capW, capH int) []byte {
	cells := Encode(n)
	cs := outW / Cells
	y := make([]byte, capW*capH)
	for cy := 0; cy < capH; cy++ {
		oy := cy * outH / capH
		for cx := 0; cx < capW; cx++ {
			ox := cx * outW / capW
			if oy < cs && ox/cs < Cells && cells[ox/cs] {
				y[cy*capW+cx] = 235
			} else {
				y[cy*capW+cx] = 16
			}
		}
	}
	return y
}

func TestDecodeScaled(t *testing.T) {
	cases := []Geometry{
		{OutW: 3840, OutH: 2160, CapW: 1280, CapH: 720},
		{OutW: 1920, OutH: 1080, CapW: 1920, CapH: 1080},
		{OutW: 1280, OutH: 720, CapW: 1920, CapH: 1080},
		{OutW: 2560, OutH: 1440, CapW: 1280, CapH: 720},
		{OutW: 1920, OutH: 1200, CapW: 1280, CapH: 720}, // aspect stretch
	}
	for _, g := range cases {
		for _, n := range []uint32{1, 777, 0xFEDCBA} {
			got, ok := g.Decode(render(n, g.OutW, g.OutH, g.CapW, g.CapH), g.CapW)
			if !ok || got != n {
				t.Errorf("%+v frame %d: got %d ok=%v", g, n, got, ok)
			}
		}
	}
}

func TestDecodeRejectsBlank(t *testing.T) {
	g := Geometry{OutW: 1920, OutH: 1080, CapW: 1280, CapH: 720}
	if _, ok := g.Decode(make([]byte, 1280*720), 1280); ok {
		t.Fatal("black picture decoded")
	}
}

func TestNextFrameSkipsCodeZero(t *testing.T) {
	if NextFrame(0) != 1 || NextFrame(Mask) != Mask+2 || NextFrame(5) != 6 {
		t.Fatal("NextFrame")
	}
}
