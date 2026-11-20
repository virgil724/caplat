//go:build windows

// Package present shows a frame-counter barcode on one output, one new frame
// per refresh, and reports the vblank time at which each frame started
// scanning out (IDXGISwapChain::GetFrameStatistics).
package present

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/virgil724/caplat/internal/barcode"
	"github.com/virgil724/caplat/internal/displays"
	"github.com/virgil724/caplat/internal/win"
)

// ErrAborted is returned when the user pressed Esc or clicked the window.
var ErrAborted = errors.New("aborted from the measurement window (Esc or click)")

// Config controls a run.
type Config struct {
	Display    displays.Display
	WarmFrames int // frames showing code 0 before measuring starts
	// Frames is the number of measured frames (0 = until ctx ends).
	Frames uint64
	// OnVblank receives (frame, vblank QPC) for measured frames.
	OnVblank func(frame uint64, qpc int64)
	// OnStart is called when the first measured frame is presented.
	OnStart func()
}

// The window procedure is a single callback (Windows limits how many can be
// created); it reports to whichever presenter is running.
var (
	procOnce  sync.Once
	aborted   atomic.Bool
	wndProcCB uintptr
)

func wndProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case win.WM_KEYDOWN:
		if wp == win.VK_ESCAPE {
			aborted.Store(true)
		}
		return 0
	case win.WM_LBUTTONDOWN, win.WM_RBUTTONDOWN, win.WM_CLOSE:
		aborted.Store(true)
		return 0
	}
	return win.DefWindowProc(hwnd, msg, wp, lp)
}

// Run presents frames on cfg.Display until cfg.Frames are done, ctx ends, or
// the user aborts. It locks its goroutine to an OS thread for the window.
func Run(ctx context.Context, cfg Config) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	procOnce.Do(func() { wndProcCB = win.NewWndProc(wndProc) })
	aborted.Store(false)

	f, err := win.CreateFactory()
	if err != nil {
		return fmt.Errorf("DXGI factory: %w", err)
	}
	defer f.Release()
	adapter, err := findAdapter(f, cfg.Display.Name)
	if err != nil {
		return err
	}
	defer adapter.Release()
	dev, ctx3d, err := win.CreateDevice(adapter)
	if err != nil {
		return fmt.Errorf("D3D11 device: %w", err)
	}
	defer dev.Release()
	defer ctx3d.Release()

	r := cfg.Display.Rect
	w, h := int(r.Right-r.Left), int(r.Bottom-r.Top)
	hwnd, err := win.CreatePopup("caplat-presenter", r, wndProcCB)
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}
	defer win.DestroyWindow(hwnd)

	sc, err := f.CreateSwapChainForHwnd(dev.Object, hwnd, &win.SwapChainDesc1{
		Width: uint32(w), Height: uint32(h), Format: win.DXGI_FORMAT_B8G8R8A8_UNORM,
		SampleCount: 1, BufferUsage: win.DXGI_USAGE_RENDER_TARGET_OUTPUT, BufferCount: 2,
		Scaling: win.DXGI_SCALING_STRETCH, SwapEffect: win.DXGI_SWAP_EFFECT_FLIP_DISCARD,
	})
	if err != nil {
		return fmt.Errorf("swap chain: %w", err)
	}
	defer sc.Release()

	cell := w / barcode.Cells
	band := make([]byte, w*cell*4) // BGRA, one row of cells
	box := &win.Box{Right: uint32(w), Bottom: uint32(cell), Back: 1}
	black := [4]float32{0, 0, 0, 1}

	// presentFrame maps a present count to the frame shown by that Present.
	const ring = 4096
	var presentFrame [ring]uint64
	var lastStat uint32
	var frame, measured uint64

	win.KeyPressed(win.VK_ESCAPE) // clear a press from before the run
	for i := 0; ; i++ {
		win.PumpMessages()
		// Esc works even when the barcode window did not get the keyboard
		// focus (Windows often refuses SetForegroundWindow to a process that
		// is not in the foreground).
		if aborted.Load() || win.KeyPressed(win.VK_ESCAPE) {
			return ErrAborted
		}
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
		drawBand(band, w, cell, barcode.Encode(uint32(show&barcode.Mask)))

		tex, err := sc.GetBuffer(0)
		if err != nil {
			return fmt.Errorf("get back buffer: %w", err)
		}
		rtv, err := dev.CreateRenderTargetView(tex)
		if err != nil {
			tex.Release()
			return fmt.Errorf("render target view: %w", err)
		}
		ctx3d.ClearRenderTargetView(rtv, &black)
		ctx3d.UpdateSubresource(tex, box, band, uint32(w*4))
		rtv.Release()
		tex.Release()

		if err := sc.Present(1, 0); err != nil {
			return fmt.Errorf("present: %w (was the display unplugged?)", err)
		}
		if pc, err := sc.GetLastPresentCount(); err == nil {
			presentFrame[pc%ring] = show
		}
		if st, err := sc.GetFrameStatistics(); err == nil && st.PresentCount != lastStat {
			lastStat = st.PresentCount
			if f := presentFrame[st.PresentCount%ring]; f != 0 && cfg.OnVblank != nil {
				cfg.OnVblank(f, st.SyncQPCTime)
			}
		}
	}
}

func drawBand(band []byte, w, cell int, cells [barcode.Cells]bool) {
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
		copy(band[y*w*4:], row)
	}
}

func findAdapter(f win.Factory, name string) (win.Object, error) {
	for i := uint32(0); ; i++ {
		ad, err := f.EnumAdapters1(i)
		if err != nil {
			return 0, fmt.Errorf("output %s not found", name)
		}
		for j := uint32(0); ; j++ {
			o, err := win.EnumOutputs(ad, j)
			if err != nil {
				break
			}
			d, err := win.OutputGetDesc(o)
			o.Release()
			if err == nil && utf16Equal(d.DeviceName[:], name) {
				return ad, nil
			}
		}
		ad.Release()
	}
}

func utf16Equal(u []uint16, s string) bool {
	i := 0
	for _, r := range s {
		if i >= len(u) || rune(u[i]) != r {
			return false
		}
		i++
	}
	return i == len(u) || u[i] == 0
}
