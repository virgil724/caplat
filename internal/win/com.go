//go:build windows

// Package win is a minimal pure-Go (no cgo) binding to the parts of COM,
// DXGI, Direct3D 11, Win32 and the DisplayConfig API that caplat needs.
// COM methods are called by vtable index; the indices come from the method
// order in the Windows SDK / mingw-w64 headers (dxgi.h, dxgi1_2.h, d3d11.h).
package win

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// GUID is a COM interface identifier.
type GUID = windows.GUID

// Object is a COM interface pointer.
type Object uintptr

// method returns the address of vtable entry i.
func (o Object) method(i int) uintptr {
	// o points at the object, whose first word points at the vtable. Both are
	// reinterpreted through pointers (not uintptr arithmetic): COM memory is
	// not Go memory, so the GC never moves it.
	obj := *(**uintptr)(unsafe.Pointer(&o))
	vtbl := *(**[1 << 10]uintptr)(unsafe.Pointer(obj))
	return vtbl[i]
}

// call invokes vtable entry i with o as the first argument. Callers pass
// pointers as uintptr(unsafe.Pointer(&x)) directly in the argument list;
// go:uintptrescapes moves each referent to the heap and keeps it alive for
// the call. Without it, a goroutine stack that grows during the call moves
// &x, and COM writes through the stale address.
//
//go:uintptrescapes
func (o Object) call(i int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.method(i), append([]uintptr{uintptr(o)}, args...)...)
	return r
}

// Release drops a reference (IUnknown::Release).
func (o Object) Release() {
	if o != 0 {
		o.call(2)
	}
}

// QueryInterface asks for another interface (IUnknown::QueryInterface).
func (o Object) QueryInterface(iid *GUID) (Object, error) {
	var out Object
	if err := hr(o.call(0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))); err != nil {
		return 0, err
	}
	return out, nil
}

// HRESULT is a COM status code.
type HRESULT uint32

func (h HRESULT) Error() string { return fmt.Sprintf("HRESULT 0x%08X", uint32(h)) }

// Known HRESULTs.
const (
	DXGI_ERROR_NOT_FOUND                 HRESULT = 0x887A0002
	DXGI_ERROR_FRAME_STATISTICS_DISJOINT HRESULT = 0x887A000B
	DXGI_ERROR_DEVICE_REMOVED            HRESULT = 0x887A0005
)

func hr(r uintptr) error {
	if int32(r) < 0 {
		return HRESULT(r)
	}
	return nil
}
