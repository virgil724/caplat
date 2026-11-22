package wayland

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Core Wayland, xdg-shell, and wp_presentation protocol constants and opcodes.
const (
	wlDisplayID = uint32(1)

	// wl_display opcodes.
	wlDisplayReqSync        = uint16(0)
	wlDisplayReqGetRegistry = uint16(1)
	wlDisplayEvError        = uint16(0)
	wlDisplayEvDeleteID     = uint16(1)

	// wl_registry opcodes.
	wlRegistryReqBind        = uint16(0)
	wlRegistryEvGlobal       = uint16(0)
	wlRegistryEvGlobalRemove = uint16(1)

	// wl_callback opcodes.
	wlCallbackEvDone = uint16(0)

	// wl_compositor opcodes.
	wlCompositorReqCreateSurface = uint16(0)
	wlCompositorReqCreateRegion  = uint16(1)

	// wl_shm opcodes & formats.
	wlShmReqCreatePool = uint16(0)
	wlShmEvFormat      = uint16(0)
	wlShmFormatARGB888 = uint32(0)
	wlShmFormatXRGB888 = uint32(1)

	// wl_shm_pool opcodes.
	wlShmPoolReqCreateBuffer = uint16(0)
	wlShmPoolReqDestroy      = uint16(1)

	// wl_buffer opcodes.
	wlBufferReqDestroy = uint16(0)
	wlBufferEvRelease  = uint16(0)

	// wl_surface opcodes.
	wlSurfaceReqDestroy         = uint16(0)
	wlSurfaceReqAttach          = uint16(1)
	wlSurfaceReqDamage          = uint16(2)
	wlSurfaceReqFrame           = uint16(3)
	wlSurfaceReqSetOpaqueRegion = uint16(4)
	wlSurfaceReqCommit          = uint16(6)
	wlSurfaceReqSetBufferScale  = uint16(8)
	wlSurfaceReqDamageBuffer    = uint16(9)

	// wl_region opcodes.
	wlRegionReqDestroy = uint16(0)
	wlRegionReqAdd     = uint16(1)

	// wl_output opcodes.
	wlOutputReqRelease    = uint16(0)
	wlOutputEvGeometry    = uint16(0)
	wlOutputEvMode        = uint16(1)
	wlOutputEvDone        = uint16(2)
	wlOutputEvScale       = uint16(3)
	wlOutputEvName        = uint16(4)
	wlOutputEvDescription = uint16(5)
	wlOutputModeCurrent   = uint32(0x1)

	// wl_seat opcodes & capabilities.
	wlSeatReqGetPointer   = uint16(0)
	wlSeatReqGetKeyboard  = uint16(1)
	wlSeatReqRelease      = uint16(3)
	wlSeatEvCapabilities  = uint16(0)
	wlSeatCapPointer      = uint32(1)
	wlSeatCapKeyboard     = uint32(2)

	// wl_pointer opcodes.
	wlPointerReqRelease = uint16(1)
	wlPointerEvButton   = uint16(3)

	// wl_keyboard opcodes & Linux KEY_ESC.
	wlKeyboardReqRelease = uint16(0)
	wlKeyboardEvKeymap   = uint16(0)
	wlKeyboardEvKey      = uint16(3)
	linuxKeyEsc          = uint32(1)
	wlInputStatePressed  = uint32(1)

	// xdg_wm_base opcodes.
	xdgWmBaseReqDestroy       = uint16(0)
	xdgWmBaseReqGetXdgSurface = uint16(2)
	xdgWmBaseReqPong          = uint16(3)
	xdgWmBaseEvPing           = uint16(0)

	// xdg_surface opcodes.
	xdgSurfaceReqDestroy      = uint16(0)
	xdgSurfaceReqGetToplevel  = uint16(1)
	xdgSurfaceReqAckConfigure = uint16(4)
	xdgSurfaceEvConfigure     = uint16(0)

	// xdg_toplevel opcodes.
	xdgToplevelReqDestroy       = uint16(0)
	xdgToplevelReqSetTitle      = uint16(2)
	xdgToplevelReqSetAppID      = uint16(3)
	xdgToplevelReqSetFullscreen = uint16(11)
	xdgToplevelEvConfigure      = uint16(0)
	xdgToplevelEvClose          = uint16(1)

	// wp_presentation opcodes & clock.
	wpPresentationReqDestroy  = uint16(0)
	wpPresentationReqFeedback = uint16(1)
	wpPresentationEvClockID   = uint16(0)
	clockMonotonic            = uint32(1)

	// wp_presentation_feedback opcodes & kind flags.
	wpFeedbackEvSyncOutput = uint16(0)
	wpFeedbackEvPresented  = uint16(1)
	wpFeedbackEvDiscarded  = uint16(2)

	FeedbackVsync        = uint32(0x1)
	FeedbackHWClock      = uint32(0x2)
	FeedbackHWCompletion = uint32(0x4)
	FeedbackZeroCopy     = uint32(0x8)
)

// Encoder builds Wayland wire protocol messages and associated SCM_RIGHTS FDs.
type Encoder struct {
	buf []byte
	fds []int
}

// Reset clears the encoded message bytes and FD list while retaining capacity.
func (e *Encoder) Reset() {
	e.buf = e.buf[:0]
	e.fds = e.fds[:0]
}

// Bytes returns the serialized wire bytes and attached FDs.
func (e *Encoder) Bytes() ([]byte, []int) {
	return e.buf, e.fds
}

// Msg appends a complete Wayland message for objID and opcode, building its
// arguments with fn and automatically patching the 16-bit message length.
func (e *Encoder) Msg(objID uint32, opcode uint16, fn func(*Encoder)) {
	start := len(e.buf)
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], objID)
	e.buf = append(e.buf, hdr[:]...)
	if fn != nil {
		fn(e)
	}
	size := len(e.buf) - start
	if size > math.MaxUint16 {
		panic(fmt.Sprintf("wayland message too large: %d bytes", size))
	}
	binary.LittleEndian.PutUint32(e.buf[start+4:start+8], (uint32(size)<<16)|uint32(opcode))
}

func (e *Encoder) Uint32(v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	e.buf = append(e.buf, b[:]...)
}

func (e *Encoder) Int32(v int32) {
	e.Uint32(uint32(v))
}

func (e *Encoder) String(s string) {
	if s == "" {
		e.Uint32(1)
		e.buf = append(e.buf, 0, 0, 0, 0)
		return
	}
	n := len(s) + 1
	e.Uint32(uint32(n))
	e.buf = append(e.buf, s...)
	e.buf = append(e.buf, 0)
	for len(e.buf)%4 != 0 {
		e.buf = append(e.buf, 0)
	}
}

func (e *Encoder) NullString() {
	e.Uint32(0)
}

func (e *Encoder) FD(fd int) {
	e.fds = append(e.fds, fd)
}

// Decoder reads arguments from a single Wayland event payload (after the 8-byte header).
type Decoder struct {
	b   []byte
	off int
}

func NewDecoder(payload []byte) *Decoder {
	return &Decoder{b: payload}
}

func (d *Decoder) Remaining() int { return len(d.b) - d.off }

func (d *Decoder) Uint32() (uint32, error) {
	if d.off+4 > len(d.b) {
		return 0, fmt.Errorf("wayland decode: short uint32 (need 4, have %d)", len(d.b)-d.off)
	}
	v := binary.LittleEndian.Uint32(d.b[d.off : d.off+4])
	d.off += 4
	return v, nil
}

func (d *Decoder) Int32() (int32, error) {
	v, err := d.Uint32()
	return int32(v), err
}

func (d *Decoder) String() (string, error) {
	n, err := d.Uint32()
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	padded := int((uint64(n) + 3) &^ 3)
	if padded < 0 || d.off+padded > len(d.b) {
		return "", fmt.Errorf("wayland decode: short string (len=%d padded=%d have=%d)", n, padded, len(d.b)-d.off)
	}
	raw := d.b[d.off : d.off+int(n)]
	d.off += padded
	if len(raw) > 0 && raw[len(raw)-1] == 0 {
		raw = raw[:len(raw)-1]
	}
	return string(raw), nil
}

func (d *Decoder) Array() ([]byte, error) {
	n, err := d.Uint32()
	if err != nil {
		return nil, err
	}
	padded := int((uint64(n) + 3) &^ 3)
	if padded < 0 || d.off+padded > len(d.b) {
		return nil, fmt.Errorf("wayland decode: short array (len=%d padded=%d have=%d)", n, padded, len(d.b)-d.off)
	}
	out := d.b[d.off : d.off+int(n)]
	d.off += padded
	return out, nil
}

// ParseHeader parses an 8-byte Wayland wire header.
func ParseHeader(b []byte) (objID uint32, opcode uint16, size int, err error) {
	if len(b) < 8 {
		return 0, 0, 0, fmt.Errorf("wayland header < 8 bytes")
	}
	objID = binary.LittleEndian.Uint32(b[0:4])
	word := binary.LittleEndian.Uint32(b[4:8])
	size = int(word >> 16)
	opcode = uint16(word & 0xffff)
	if size < 8 || size%4 != 0 {
		return 0, 0, 0, fmt.Errorf("wayland invalid message size %d (obj=%d op=%d)", size, objID, opcode)
	}
	return objID, opcode, size, nil
}
