//go:build windows

package win

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WASAPI: just enough to list audio endpoints, play and capture shared-mode
// streams event-driven, and read the playback position with its QPC time.
// Vtable indices come from the method order in mmdeviceapi.h,
// propsys.h and audioclient.h (mingw-w64).

var procPropVariantClear = modOle32.NewProc("PropVariantClear")

// WASAPI GUIDs.
var (
	CLSID_MMDeviceEnumerator = guid(0xbcde0395, 0xe52f, 0x467c, 0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e)
	IID_IMMDeviceEnumerator  = guid(0xa95664d2, 0x9614, 0x4f35, 0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6)
	IID_IAudioClient         = guid(0x1cb9ad4c, 0xdbfa, 0x4c32, 0xb1, 0x78, 0xc2, 0xf5, 0x68, 0xa7, 0x03, 0xb2)
	IID_IAudioRenderClient   = guid(0xf294acfc, 0x3146, 0x4483, 0xa7, 0xbf, 0xad, 0xdc, 0xa7, 0xc2, 0x60, 0xe2)
	IID_IAudioCaptureClient  = guid(0xc8adbd64, 0xe71e, 0x48a0, 0xa4, 0xde, 0x18, 0x5c, 0x39, 0x5c, 0xd3, 0x17)
	IID_IAudioClock          = guid(0xcd63314f, 0x3fba, 0x4a1b, 0x81, 0x2c, 0xef, 0x96, 0x35, 0x87, 0x28, 0xe7)
	pkeyDeviceFriendlyName   = propertyKey{guid(0xa45c254e, 0xdf1c, 0x4efd, 0x80, 0x20, 0x67, 0xd1, 0x46, 0xa8, 0x50, 0xe0), 14}
	subtypeIEEEFloat         = guid(0x00000003, 0x0000, 0x0010, 0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71)
	subtypePCM               = guid(0x00000001, 0x0000, 0x0010, 0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71)
)

// Data flows.
const (
	ERender  = 0
	ECapture = 1
)

// Capture buffer flags.
const (
	BufferFlagsDiscontinuity = 0x1
	BufferFlagsSilent        = 0x2
	BufferFlagsTimestampErr  = 0x4
)

const (
	vtEnumAudioEndpoints = 3 // IMMDeviceEnumerator
	vtCollectionCount    = 3 // IMMDeviceCollection
	vtCollectionItem     = 4
	vtDeviceActivate     = 3 // IMMDevice
	vtOpenPropertyStore  = 4
	vtPropStoreGetValue  = 5 // IPropertyStore
	vtACInitialize       = 3 // IAudioClient
	vtACGetBufferSize    = 4
	vtACGetPadding       = 6
	vtACGetMixFormat     = 8
	vtACStart            = 10
	vtACStop             = 11
	vtACSetEventHandle   = 13
	vtACGetService       = 14
	vtRCGetBuffer        = 3 // IAudioRenderClient
	vtRCReleaseBuffer    = 4
	vtCCGetBuffer        = 3 // IAudioCaptureClient
	vtCCReleaseBuffer    = 4
	vtCCNextPacketSize   = 5
	vtClockFrequency     = 3 // IAudioClock
	vtClockPosition      = 4
)

type propertyKey struct {
	Fmtid GUID
	Pid   uint32
}

// propVariant is a PROPVARIANT holding a string (24 bytes on 64-bit).
type propVariant struct {
	VT  uint16
	_   [3]uint16
	Str *uint16
	_   uintptr
}

// Endpoint is an active audio endpoint; Release Device.
type Endpoint struct {
	Name   string
	Device Object
}

// AudioEndpoints lists the active endpoints of a flow (ERender or ECapture).
func AudioEndpoints(flow int) ([]Endpoint, error) {
	en, err := CoCreate(&CLSID_MMDeviceEnumerator, &IID_IMMDeviceEnumerator)
	if err != nil {
		return nil, err
	}
	defer en.Release()
	var col Object
	if err := hr(en.call(vtEnumAudioEndpoints, uintptr(flow), 1, uintptr(unsafe.Pointer(&col)))); err != nil { // DEVICE_STATE_ACTIVE
		return nil, err
	}
	defer col.Release()
	var n uint32
	if err := hr(col.call(vtCollectionCount, uintptr(unsafe.Pointer(&n)))); err != nil {
		return nil, err
	}
	var out []Endpoint
	for i := uint32(0); i < n; i++ {
		var d Object
		if hr(col.call(vtCollectionItem, uintptr(i), uintptr(unsafe.Pointer(&d)))) != nil {
			continue
		}
		out = append(out, Endpoint{Name: endpointName(d), Device: d})
	}
	return out, nil
}

func endpointName(d Object) string {
	var ps Object
	if hr(d.call(vtOpenPropertyStore, 0, uintptr(unsafe.Pointer(&ps)))) != nil { // STGM_READ
		return ""
	}
	defer ps.Release()
	var pv propVariant
	if hr(ps.call(vtPropStoreGetValue, uintptr(unsafe.Pointer(&pkeyDeviceFriendlyName)), uintptr(unsafe.Pointer(&pv)))) != nil {
		return ""
	}
	defer procPropVariantClear.Call(uintptr(unsafe.Pointer(&pv)))
	if pv.VT != 31 || pv.Str == nil { // VT_LPWSTR
		return ""
	}
	return windows.UTF16PtrToString(pv.Str)
}

// WaveFormat describes a stream's samples.
type WaveFormat struct {
	Channels   int
	Rate       int
	Bits       int  // container bits per sample
	Float      bool // IEEE float, else integer PCM
	BlockAlign int  // bytes per frame
}

func parseWaveFormat(p *byte) (WaveFormat, error) {
	head := unsafe.Slice(p, 18)
	tag := binary.LittleEndian.Uint16(head[0:])
	f := WaveFormat{
		Channels:   int(binary.LittleEndian.Uint16(head[2:])),
		Rate:       int(binary.LittleEndian.Uint32(head[4:])),
		BlockAlign: int(binary.LittleEndian.Uint16(head[12:])),
		Bits:       int(binary.LittleEndian.Uint16(head[14:])),
	}
	switch tag {
	case 1: // WAVE_FORMAT_PCM
	case 3: // WAVE_FORMAT_IEEE_FLOAT
		f.Float = true
	case 0xfffe: // WAVE_FORMAT_EXTENSIBLE
		ext := unsafe.Slice(p, 40)
		var sub GUID
		sub.Data1 = binary.LittleEndian.Uint32(ext[24:])
		sub.Data2 = binary.LittleEndian.Uint16(ext[28:])
		sub.Data3 = binary.LittleEndian.Uint16(ext[30:])
		copy(sub.Data4[:], ext[32:40])
		switch sub {
		case subtypeIEEEFloat:
			f.Float = true
		case subtypePCM:
		default:
			return f, fmt.Errorf("unsupported sample format %v", sub)
		}
	default:
		return f, fmt.Errorf("unsupported wave format tag 0x%x", tag)
	}
	if f.Float && f.Bits != 32 || !f.Float && f.Bits != 16 && f.Bits != 24 && f.Bits != 32 {
		return f, fmt.Errorf("unsupported %d-bit samples", f.Bits)
	}
	return f, nil
}

// AudioClient is an initialized shared-mode, event-driven IAudioClient.
type AudioClient struct {
	Object
	Format WaveFormat
	Frames int // buffer size in frames
	Event  windows.Handle
}

// NewAudioClient activates and initializes an endpoint in shared mode with
// its mix format, signalling Event each period. buffer is in 100 ns units.
func NewAudioClient(device Object, buffer int64) (*AudioClient, error) {
	var ac Object
	if err := hr(device.call(vtDeviceActivate, uintptr(unsafe.Pointer(&IID_IAudioClient)), 1, 0, uintptr(unsafe.Pointer(&ac)))); err != nil {
		return nil, fmt.Errorf("activate: %w", err)
	}
	c := &AudioClient{Object: ac}
	var mix *byte
	if err := hr(ac.call(vtACGetMixFormat, uintptr(unsafe.Pointer(&mix)))); err != nil {
		ac.Release()
		return nil, fmt.Errorf("mix format: %w", err)
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(mix)))
	f, err := parseWaveFormat(mix)
	if err != nil {
		ac.Release()
		return nil, err
	}
	c.Format = f
	const eventCallback = 0x00040000
	if err := hr(ac.call(vtACInitialize, 0, eventCallback, uintptr(buffer), 0, uintptr(unsafe.Pointer(mix)), 0)); err != nil {
		ac.Release()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	var frames uint32
	ac.call(vtACGetBufferSize, uintptr(unsafe.Pointer(&frames)))
	c.Frames = int(frames)
	ev, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		ac.Release()
		return nil, err
	}
	c.Event = ev
	if err := hr(ac.call(vtACSetEventHandle, uintptr(ev))); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Close releases the client and its event.
func (c *AudioClient) Close() {
	c.Release()
	windows.CloseHandle(c.Event)
}

func (c *AudioClient) Start() error { return hr(c.call(vtACStart)) }
func (c *AudioClient) Stop() error  { return hr(c.call(vtACStop)) }

// Padding is the number of frames queued for playback.
func (c *AudioClient) Padding() (int, error) {
	var n uint32
	err := hr(c.call(vtACGetPadding, uintptr(unsafe.Pointer(&n))))
	return int(n), err
}

// Service returns a service interface (render/capture client, clock).
func (c *AudioClient) Service(iid *GUID) (Object, error) {
	var o Object
	err := hr(c.call(vtACGetService, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&o))))
	return o, err
}

// RenderBuffer returns room for n frames; commit with RenderRelease.
func RenderBuffer(rc Object, n, blockAlign int) ([]byte, error) {
	var p *byte
	if err := hr(rc.call(vtRCGetBuffer, uintptr(n), uintptr(unsafe.Pointer(&p)))); err != nil {
		return nil, err
	}
	return unsafe.Slice(p, n*blockAlign), nil
}

// RenderRelease commits n frames written to the last RenderBuffer.
func RenderRelease(rc Object, n int) error { return hr(rc.call(vtRCReleaseBuffer, uintptr(n), 0)) }

// CapturePacket is one packet read from a capture client.
type CapturePacket struct {
	Data   []byte // valid until CaptureRelease
	Frames int
	Flags  uint32
	QPC    int64 // when the first frame was captured, in 100 ns units of QPC time
}

// NextPacketSize is the size of the next capture packet in frames.
func NextPacketSize(cc Object) (int, error) {
	var n uint32
	err := hr(cc.call(vtCCNextPacketSize, uintptr(unsafe.Pointer(&n))))
	return int(n), err
}

// CaptureGet reads the next packet; release it with CaptureRelease.
func CaptureGet(cc Object, blockAlign int) (CapturePacket, error) {
	var p *byte
	var frames, flags uint32
	var devPos, qpc uint64
	if err := hr(cc.call(vtCCGetBuffer, uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&frames)),
		uintptr(unsafe.Pointer(&flags)), uintptr(unsafe.Pointer(&devPos)), uintptr(unsafe.Pointer(&qpc)))); err != nil {
		return CapturePacket{}, err
	}
	pk := CapturePacket{Frames: int(frames), Flags: flags, QPC: int64(qpc)}
	if p != nil && frames > 0 {
		pk.Data = unsafe.Slice(p, int(frames)*blockAlign)
	}
	return pk, nil
}

// CaptureRelease releases the last packet.
func CaptureRelease(cc Object, frames int) error {
	return hr(cc.call(vtCCReleaseBuffer, uintptr(frames)))
}

// ClockFrequency and ClockPosition read an IAudioClock: the position of
// the sample the device is playing, in units of the frequency, and the QPC
// time (100 ns units) of that position.
func ClockFrequency(clk Object) (uint64, error) {
	var f uint64
	err := hr(clk.call(vtClockFrequency, uintptr(unsafe.Pointer(&f))))
	return f, err
}

func ClockPosition(clk Object) (pos, qpc uint64, err error) {
	err = hr(clk.call(vtClockPosition, uintptr(unsafe.Pointer(&pos)), uintptr(unsafe.Pointer(&qpc))))
	return pos, qpc, err
}
