package present

import (
	"context"
	"errors"
	"testing"

	"github.com/virgil724/caplat/internal/barcode"
	"github.com/virgil724/caplat/displays"
)

type fakePresenter struct {
	openCount  int
	closeCount int
	opened     bool
	closed     bool
	frames     []uint64
	onVblank   func(uint64, int64)
	abortAt    int
}

func (f *fakePresenter) Open(_ displays.Display, onVblank func(uint64, int64)) error {
	f.openCount++
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
	f.closeCount++
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

func TestRunSessionMultiSegment(t *testing.T) {
	p := &fakePresenter{}
	segs := make(chan Segment)
	sessErr := make(chan error, 1)
	go func() {
		sessErr <- RunSession(context.Background(), displays.Display{Width: 1280, Height: 720}, p, segs)
	}()

	var vb1, vb2 []uint64
	var starts int
	d1 := make(chan error, 1)
	segs <- Segment{
		WarmFrames: 2,
		Frames:     3,
		Trailing:   2,
		OnVblank:   func(f uint64, _ int64) { vb1 = append(vb1, f) },
		OnStart:    func() { starts++ },
		Done:       d1,
	}
	if err := <-d1; err != nil {
		t.Fatalf("seg 1: %v", err)
	}

	d2 := make(chan error, 1)
	segs <- Segment{
		WarmFrames: 1,
		Frames:     2,
		Trailing:   0,
		OnVblank:   func(f uint64, _ int64) { vb2 = append(vb2, f) },
		OnStart:    func() { starts++ },
		Done:       d2,
	}
	if err := <-d2; err != nil {
		t.Fatalf("seg 2: %v", err)
	}
	close(segs)
	if err := <-sessErr; err != nil {
		t.Fatalf("RunSession: %v", err)
	}

	if p.openCount != 1 || p.closeCount != 1 {
		t.Fatalf("openCount=%d closeCount=%d, want 1/1", p.openCount, p.closeCount)
	}
	if starts != 2 {
		t.Fatalf("starts = %d, want 2", starts)
	}
	wantFrames := []uint64{0, 0, 1, 2, 3, 0, 0, 0, 1, 2}
	if len(p.frames) != len(wantFrames) {
		t.Fatalf("frames = %v, want %v", p.frames, wantFrames)
	}
	for i := range wantFrames {
		if p.frames[i] != wantFrames[i] {
			t.Fatalf("frames = %v, want %v", p.frames, wantFrames)
		}
	}
	if len(vb1) != 3 || vb1[0] != 1 || vb1[2] != 3 {
		t.Errorf("vb1 = %v, want [1 2 3]", vb1)
	}
	if len(vb2) != 2 || vb2[0] != 1 || vb2[1] != 2 {
		t.Errorf("vb2 = %v, want [1 2]", vb2)
	}
}

func TestRunSessionAbortAndCancel(t *testing.T) {
	t.Run("abort", func(t *testing.T) {
		p := &fakePresenter{abortAt: 2}
		segs := make(chan Segment)
		sessErr := make(chan error, 1)
		go func() {
			sessErr <- RunSession(context.Background(), displays.Display{}, p, segs)
		}()
		d := make(chan error, 1)
		segs <- Segment{WarmFrames: 5, Done: d}
		if err := <-d; !errors.Is(err, ErrAborted) {
			t.Fatalf("seg err = %v, want ErrAborted", err)
		}
		if err := <-sessErr; !errors.Is(err, ErrAborted) {
			t.Fatalf("session err = %v, want ErrAborted", err)
		}
		if !p.closed {
			t.Fatal("presenter was not closed on abort")
		}
	})

	t.Run("segment_cancel", func(t *testing.T) {
		p := &fakePresenter{}
		segs := make(chan Segment)
		sessErr := make(chan error, 1)
		go func() {
			sessErr <- RunSession(context.Background(), displays.Display{}, p, segs)
		}()
		segCtx, segCancel := context.WithCancel(context.Background())
		segCancel()
		d := make(chan error, 1)
		segs <- Segment{Ctx: segCtx, WarmFrames: 5, Frames: 10, Done: d}
		if err := <-d; err != nil {
			t.Fatalf("canceled seg err = %v, want nil", err)
		}
		close(segs)
		if err := <-sessErr; err != nil {
			t.Fatalf("session err = %v, want nil", err)
		}
		if p.closeCount != 1 {
			t.Fatalf("closeCount = %d, want 1", p.closeCount)
		}
	})
}
