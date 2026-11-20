//go:build windows

package win

import "unsafe"

// vtable indices (method order in d3d11.h).
const (
	vtDeviceCreateRenderTargetView = 9
	vtContextUpdateSubresource     = 48
	vtContextClearRenderTargetView = 50
	vtContextFlush                 = 111
)

const (
	d3dDriverTypeUnknown = 0
	d3d11SDKVersion      = 7
)

// Box is D3D11_BOX.
type Box struct{ Left, Top, Front, Right, Bottom, Back uint32 }

// Device is ID3D11Device; Context is its immediate ID3D11DeviceContext.
type Device struct{ Object }
type Context struct{ Object }

// CreateDevice creates a D3D11 device on the given adapter.
func CreateDevice(adapter Object) (Device, Context, error) {
	var dev, ctx Object
	r, _, _ := procD3D11CreateDevice.Call(uintptr(adapter), d3dDriverTypeUnknown, 0, 0, 0, 0,
		d3d11SDKVersion, uintptr(unsafe.Pointer(&dev)), 0, uintptr(unsafe.Pointer(&ctx)))
	if err := hr(r); err != nil {
		return Device{}, Context{}, err
	}
	return Device{dev}, Context{ctx}, nil
}

// CreateRenderTargetView creates a view of a texture with its own format.
func (d Device) CreateRenderTargetView(resource Object) (Object, error) {
	var v Object
	err := hr(d.call(vtDeviceCreateRenderTargetView, uintptr(resource), 0, uintptr(unsafe.Pointer(&v))))
	return v, err
}

// ClearRenderTargetView fills a view with one colour.
func (c Context) ClearRenderTargetView(view Object, rgba *[4]float32) {
	c.call(vtContextClearRenderTargetView, uintptr(view), uintptr(unsafe.Pointer(rgba)))
}

// UpdateSubresource copies CPU data into a box of a resource.
func (c Context) UpdateSubresource(resource Object, box *Box, data []byte, rowPitch uint32) {
	c.call(vtContextUpdateSubresource, uintptr(resource), 0, uintptr(unsafe.Pointer(box)),
		uintptr(unsafe.Pointer(&data[0])), uintptr(rowPitch), 0)
}

// Flush submits queued commands.
func (c Context) Flush() { c.call(vtContextFlush) }
