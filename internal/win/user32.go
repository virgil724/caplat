//go:build windows

package win

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW              = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW               = modUser32.NewProc("CreateWindowExW")
	procDestroyWindow                 = modUser32.NewProc("DestroyWindow")
	procDefWindowProcW                = modUser32.NewProc("DefWindowProcW")
	procPeekMessageW                  = modUser32.NewProc("PeekMessageW")
	procTranslateMessage              = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW              = modUser32.NewProc("DispatchMessageW")
	procSetProcessDpiAwarenessContext = modUser32.NewProc("SetProcessDpiAwarenessContext")
	procSetForegroundWindow           = modUser32.NewProc("SetForegroundWindow")
	procGetAsyncKeyState              = modUser32.NewProc("GetAsyncKeyState")
	procGetModuleHandleW              = modKernel32.NewProc("GetModuleHandleW")
	procQueryPerformanceCounter       = modKernel32.NewProc("QueryPerformanceCounter")
	procQueryPerformanceFrequency     = modKernel32.NewProc("QueryPerformanceFrequency")
)

// Window messages and styles used by the presenter.
const (
	WM_DESTROY     = 0x0002
	WM_CLOSE       = 0x0010
	WM_KEYDOWN     = 0x0100
	WM_LBUTTONDOWN = 0x0201
	WM_RBUTTONDOWN = 0x0204
	VK_ESCAPE      = 0x1B

	WS_POPUP      = 0x80000000
	WS_VISIBLE    = 0x10000000
	WS_EX_TOPMOST = 0x00000008
	PM_REMOVE     = 0x0001
)

// QPC returns QueryPerformanceCounter.
func QPC() int64 {
	var t int64
	procQueryPerformanceCounter.Call(uintptr(unsafe.Pointer(&t)))
	return t
}

// QPCFrequency returns QueryPerformanceFrequency (ticks per second).
func QPCFrequency() int64 {
	var f int64
	procQueryPerformanceFrequency.Call(uintptr(unsafe.Pointer(&f)))
	return f
}

// SetDPIAware makes the process per-monitor DPI aware (v2), so desktop
// coordinates are physical pixels. It fails harmlessly if already set.
func SetDPIAware() {
	const perMonitorAwareV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	procSetProcessDpiAwarenessContext.Call(perMonitorAwareV2)
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

// Msg is the Win32 MSG structure.
type Msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
	Private uint32
}

// NewWndProc wraps a window procedure as a callback. Windows allows only a
// limited number of callbacks per process, so create it once.
func NewWndProc(proc func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr) uintptr {
	return syscall.NewCallback(proc)
}

// CreatePopup creates a borderless, topmost, visible window at r. wndProc
// comes from NewWndProc; the class is registered on first use.
func CreatePopup(class string, r RECT, wndProc uintptr) (uintptr, error) {
	inst, _, _ := procGetModuleHandleW.Call(0)
	cls, _ := windows.UTF16PtrFromString(class)
	wc := wndClassEx{
		WndProc:   wndProc,
		Instance:  inst,
		ClassName: cls,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))) // fails harmlessly if already registered
	h, _, err := procCreateWindowExW.Call(WS_EX_TOPMOST, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(cls)),
		WS_POPUP|WS_VISIBLE, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top),
		0, 0, inst, 0)
	if h == 0 {
		return 0, err
	}
	procSetForegroundWindow.Call(h)
	return h, nil
}

// KeyPressed reports whether a key is down now or was pressed since the last
// call, whichever window has the keyboard focus. The second part catches a
// tap shorter than the polling interval.
func KeyPressed(vk int) bool {
	r, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return r&0x8001 != 0
}

// DestroyWindow destroys a window.
func DestroyWindow(h uintptr) { procDestroyWindow.Call(h) }

// DefWindowProc is the default window procedure.
func DefWindowProc(h uintptr, msg uint32, wp, lp uintptr) uintptr {
	r, _, _ := procDefWindowProcW.Call(h, uintptr(msg), wp, lp)
	return r
}

// PumpMessages dispatches all pending messages of the calling thread.
func PumpMessages() {
	var m Msg
	for {
		r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, PM_REMOVE)
		if r == 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}
