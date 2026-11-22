// Package present shows a frame-counter barcode on one output, one new frame
// per refresh, and reports the vblank time at which each frame started
// scanning out.
package present

import (
	"context"
	"errors"

	"github.com/virgil724/caplat/internal/barcode"
	"github.com/virgil724/caplat/internal/displays"
)

// ErrAborted is returned when the user pressed Esc or clicked the window.
var ErrAborted = errors.New("aborted from the measurement window (Esc or click)")

// Config controls a run.
type Config struct {
	Display    displays.Display
	WarmFrames int // frames showing code 0 before measuring starts
	// Frames is the number of measured frames (0 = until ctx ends).
	Frames uint64
	// OnVblank receives (frame, vblank tick) for measured frames in platform
	// monotonic ticks (QPC on Windows, CLOCK_MONOTONIC ns on Linux).
	OnVblank func(frame uint64, qpc int64)
	// OnStart is called when the first measured frame is presented.
	OnStart func()
}

// Presenter displays barcode frames on a desktop output and reports each
// frame's scan-out vblank time via the onVblank callback passed to Open.
type Presenter interface {
	Open(d displays.Display, onVblank func(frame uint64, qpc int64)) error
	Present(frame uint64) error
	Close() error
}

// Run presents frames on cfg.Display using p until cfg.Frames are done, ctx
// ends, or the presenter aborts.
func Run(ctx context.Context, cfg Config, p Presenter) error {
	if err := p.Open(cfg.Display, cfg.OnVblank); err != nil {
		return err
	}
	defer p.Close()

	var frame, measured uint64
	for i := 0; ; i++ {
		if ctx.Err() != nil {
			return nil
		}
		show := uint64(0) // warm-up: code 0
		if i >= cfg.WarmFrames {
			if cfg.Frames > 0 && measured >= cfg.Frames {
				return nil
			}
			frame = barcode.NextFrame(frame)
			show = frame
			measured++
			if measured == 1 && cfg.OnStart != nil {
				cfg.OnStart()
			}
		}
		if err := p.Present(show); err != nil {
			return err
		}
	}
}

// DrawBand writes a barcode row of cells into a 32-bit BGRA/RGBA buffer of
// width w, height cell, and byte stride (if stride <= 0, w*4 is used).
func DrawBand(band []byte, w, stride, cell int, cells [barcode.Cells]bool) {
	if stride <= 0 {
		stride = w * 4
	}
	if w <= 0 || cell <= 0 {
		return
	}
	for x := 0; x < w; x++ {
		c := x / cell
		v := byte(0)
		if c < barcode.Cells && cells[c] {
			v = 255
		}
		p := x * 4
		band[p], band[p+1], band[p+2], band[p+3] = v, v, v, 255
	}
	row := band[:w*4]
	for y := 1; y < cell; y++ {
		copy(band[y*stride:y*stride+w*4], row)
	}
}
