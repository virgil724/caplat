package present

import (
	"context"
	"errors"
	"testing"

	"github.com/virgil724/caplat/internal/barcode"
	"github.com/virgil724/caplat/displays"
)

type fakePresenter struct {
	opened   bool
	closed   bool
	frames   []uint64
	onVblank func(uint64, int64)
	abortAt  int
}

func (f *fakePresenter) Open(_ displays.Display, onVblank func(uint64, int64)) error {
	f.opened = true
	f.onVblank = onVblank
	return nil
}

func (f *fakePresenter) Present(frame uint64) error {
	f.frames = append(f.frames, frame)
	if f.abortAt > 0 && len(f.frames) == f.abortAt {
		return ErrAborted
	}
	if frame != 0 && f.onVblank != nil {
		f.onVblank(frame, int64(frame)*1000)
	}
	return nil
}

func (f *fakePresenter) Close() error {
	f.closed = true
	return nil
}

func TestRunLoop(t *testing.T) {
	p := &fakePresenter{}
	started := 0
	var vblanks []uint64
	err := Run(context.Background(), Config{
		Display:    displays.Display{Width: 1280, Height: 720},
		WarmFrames: 3,
		Frames:     4,
		OnStart:    func() { started++ },
		OnVblank:   func(frame uint64, _ int64) { vblanks = append(vblanks, frame) },
	}, p)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !p.opened || !p.closed {
		t.Fatalf("opened=%v closed=%v", p.opened, p.closed)
	}
	if started != 1 {
		t.Fatalf("started=%d, want 1", started)
	}
	want := []uint64{0, 0, 0, 1, 2, 3, 4}
	if len(p.frames) != len(want) {
		t.Fatalf("frames=%v, want %v", p.frames, want)
	}
	for i := range want {
		if p.frames[i] != want[i] {
			t.Fatalf("frames=%v, want %v", p.frames, want)
		}
	}
	if len(vblanks) != 4 {
		t.Fatalf("vblanks=%v, want 4", vblanks)
	}
}

func TestRunAbort(t *testing.T) {
	p := &fakePresenter{abortAt: 2}
	err := Run(context.Background(), Config{WarmFrames: 5}, p)
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("err=%v, want ErrAborted", err)
	}
	if !p.closed {
		t.Fatal("presenter was not closed on abort")
	}
}

func TestDrawBandRoundTrip(t *testing.T) {
	const w, h = 1280, 720
	cell := w / barcode.Cells
	stride := w*4 + 16 // test non-tight stride
	band := make([]byte, stride*cell)
	DrawBand(band, w, stride, cell, barcode.Encode(0x123456))

	luma := make([]byte, w*h)
	for y := 0; y < cell; y++ {
		for x := 0; x < w; x++ {
			luma[y*w+x] = band[y*stride+x*4]
		}
	}
	geo := barcode.Geometry{OutW: w, OutH: h, CapW: w, CapH: h}
	got, ok := geo.Decode(luma, w)
	if !ok || got != 0x123456 {
		t.Fatalf("Decode = (%#x, %v), want (0x123456, true)", got, ok)
	}
}
