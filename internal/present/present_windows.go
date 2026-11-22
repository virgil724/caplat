//go:build windows

package present

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/virgil724/caplat/internal/barcode"
	"github.com/virgil724/caplat/displays"
	"github.com/virgil724/caplat/internal/win"
)

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

type winPresenter struct {
	f        win.Factory
	adapter  win.Object
	dev      win.Device
	ctx3d    win.Context
	hwnd     uintptr
	sc       win.SwapChain
	restore  func()
	locked   bool
	w, cell  int
	band     []byte
	box      *win.Box
	black    [4]float32
	onVblank func(frame uint64, qpc int64)

	presentFrame [4096]uint64
	lastStat     uint32
}

// New returns the Windows DXGI presenter.
func New() Presenter { return &winPresenter{} }

func (p *winPresenter) Open(d displays.Display, onVblank func(frame uint64, qpc int64)) error {
	runtime.LockOSThread()
	p.locked = true
	// SetThreadExecutionState is per thread: call it on this locked thread.
	p.restore = win.KeepAwake()
	procOnce.Do(func() { wndProcCB = win.NewWndProc(wndProc) })
	aborted.Store(false)

	var err error
	p.f, err = win.CreateFactory()
	if err != nil {
		p.Close()
		return fmt.Errorf("DXGI factory: %w", err)
	}
	p.adapter, err = findAdapter(p.f, d.Name)
	if err != nil {
		p.Close()
		return err
	}
	p.dev, p.ctx3d, err = win.CreateDevice(p.adapter)
	if err != nil {
		p.Close()
		return fmt.Errorf("D3D11 device: %w", err)
	}

	r := win.RECT(d.Rect)
	w, h := int(r.Right-r.Left), int(r.Bottom-r.Top)
	p.hwnd, err = win.CreatePopup("caplat-presenter", r, wndProcCB)
	if err != nil {
		p.Close()
		return fmt.Errorf("create window: %w", err)
	}

	p.sc, err = p.f.CreateSwapChainForHwnd(p.dev.Object, p.hwnd, &win.SwapChainDesc1{
		Width: uint32(w), Height: uint32(h), Format: win.DXGI_FORMAT_B8G8R8A8_UNORM,
		SampleCount: 1, BufferUsage: win.DXGI_USAGE_RENDER_TARGET_OUTPUT, BufferCount: 2,
		Scaling: win.DXGI_SCALING_STRETCH, SwapEffect: win.DXGI_SWAP_EFFECT_FLIP_DISCARD,
	})
	if err != nil {
		p.Close()
		return fmt.Errorf("swap chain: %w", err)
	}

	p.w = w
	p.cell = w / barcode.Cells
	p.band = make([]byte, w*p.cell*4) // BGRA, one row of cells
	p.box = &win.Box{Right: uint32(w), Bottom: uint32(p.cell), Back: 1}
	p.black = [4]float32{0, 0, 0, 1}
	p.onVblank = onVblank
	win.KeyPressed(win.VK_ESCAPE) // clear a press from before the run
	return nil
}

func (p *winPresenter) Present(show uint64) error {
	win.PumpMessages()
	// Esc works even when the barcode window did not get the keyboard
	// focus (Windows often refuses SetForegroundWindow to a process that
	// is not in the foreground).
	if aborted.Load() || win.KeyPressed(win.VK_ESCAPE) {
		return ErrAborted
	}
	DrawBand(p.band, p.w, p.w*4, p.cell, barcode.Encode(uint32(show&barcode.Mask)))

	tex, err := p.sc.GetBuffer(0)
	if err != nil {
		return fmt.Errorf("get back buffer: %w", err)
	}
	rtv, err := p.dev.CreateRenderTargetView(tex)
	if err != nil {
		tex.Release()
		return fmt.Errorf("render target view: %w", err)
	}
	p.ctx3d.ClearRenderTargetView(rtv, &p.black)
	p.ctx3d.UpdateSubresource(tex, p.box, p.band, uint32(p.w*4))
	rtv.Release()
	tex.Release()

	if err := p.sc.Present(1, 0); err != nil {
		if errors.Is(err, win.ErrOccluded) {
			return err
		}
		return fmt.Errorf("present: %w (was the display unplugged?)", err)
	}
	if pc, err := p.sc.GetLastPresentCount(); err == nil {
		p.presentFrame[pc%uint32(len(p.presentFrame))] = show
	}
	if st, err := p.sc.GetFrameStatistics(); err == nil && st.PresentCount != p.lastStat {
		p.lastStat = st.PresentCount
		if f := p.presentFrame[st.PresentCount%uint32(len(p.presentFrame))]; f != 0 && p.onVblank != nil {
			p.onVblank(f, st.SyncQPCTime)
		}
	}
	return nil
}

func (p *winPresenter) Close() error {
	if p.sc.Object != 0 {
		p.sc.Release()
		p.sc = win.SwapChain{}
	}
	if p.hwnd != 0 {
		win.DestroyWindow(p.hwnd)
		p.hwnd = 0
	}
	if p.ctx3d.Object != 0 {
		p.ctx3d.Release()
		p.ctx3d = win.Context{}
	}
	if p.dev.Object != 0 {
		p.dev.Release()
		p.dev = win.Device{}
	}
	if p.adapter != 0 {
		p.adapter.Release()
		p.adapter = 0
	}
	if p.f.Object != 0 {
		p.f.Release()
		p.f = win.Factory{}
	}
	if p.restore != nil {
		p.restore()
		p.restore = nil
	}
	if p.locked {
		runtime.UnlockOSThread()
		p.locked = false
	}
	return nil
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
