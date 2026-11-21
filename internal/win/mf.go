//go:build windows

package win

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Media Foundation: just enough to enumerate video capture devices and read
// frames synchronously with an IMFSourceReader. Vtable indices come from the
// method order in mfobjects.h, mfidl.h and mfreadwrite.h.

var (
	modMFPlat      = windows.NewLazySystemDLL("mfplat.dll")
	modMF          = windows.NewLazySystemDLL("mf.dll")
	modMFReadWrite = windows.NewLazySystemDLL("mfreadwrite.dll")
	modOle32       = windows.NewLazySystemDLL("ole32.dll")

	procMFStartup                           = modMFPlat.NewProc("MFStartup")
	procMFShutdown                          = modMFPlat.NewProc("MFShutdown")
	procMFCreateAttributes                  = modMFPlat.NewProc("MFCreateAttributes")
	procMFEnumDeviceSources                 = modMF.NewProc("MFEnumDeviceSources")
	procMFCreateSourceReaderFromMediaSource = modMFReadWrite.NewProc("MFCreateSourceReaderFromMediaSource")
	procCoInitializeEx                      = modOle32.NewProc("CoInitializeEx")
	procCoUninitialize                      = modOle32.NewProc("CoUninitialize")
	procCoTaskMemFree                       = modOle32.NewProc("CoTaskMemFree")
)

func guid(d1 uint32, d2, d3 uint16, d4 ...byte) GUID {
	g := GUID{Data1: d1, Data2: d2, Data3: d3}
	copy(g.Data4[:], d4)
	return g
}

// Media Foundation GUIDs.
var (
	IID_IMFMediaSource = guid(0x279a808d, 0xaec7, 0x40c8, 0x9c, 0x6b, 0xa6, 0xb4, 0x92, 0xc7, 0x8a, 0x66)
	IID_IMF2DBuffer    = guid(0x7dc9d5f9, 0x9ed9, 0x44ec, 0x9b, 0xbf, 0x06, 0x00, 0xbb, 0x58, 0x9f, 0xbb)

	MF_DEVSOURCE_ATTRIBUTE_SOURCE_TYPE             = guid(0xc60ac5fe, 0x252a, 0x478f, 0xa0, 0xef, 0xbc, 0x8f, 0xa5, 0xf7, 0xca, 0xd3)
	MF_DEVSOURCE_ATTRIBUTE_SOURCE_TYPE_VIDCAP_GUID = guid(0x8ac3587a, 0x4ae7, 0x42d8, 0x99, 0xe0, 0x0a, 0x60, 0x13, 0xee, 0xf9, 0x0f)
	MF_DEVSOURCE_ATTRIBUTE_FRIENDLY_NAME           = guid(0x60d0e559, 0x52f8, 0x4fa2, 0xbb, 0xce, 0xac, 0xdb, 0x34, 0xa8, 0xec, 0x01)

	MF_MT_MAJOR_TYPE                = guid(0x48eba18e, 0xf8c9, 0x4687, 0xbf, 0x11, 0x0a, 0x74, 0xc9, 0xf9, 0x6a, 0x8f)
	MF_MT_SUBTYPE                   = guid(0xf7e34c9a, 0x42e8, 0x4714, 0xb7, 0x4b, 0xcb, 0x29, 0xd7, 0x2c, 0x35, 0xe5)
	MF_MT_FRAME_SIZE                = guid(0x1652c33d, 0xd6b2, 0x4012, 0xb8, 0x34, 0x72, 0x03, 0x08, 0x49, 0xa3, 0x7d)
	MF_MT_FRAME_RATE                = guid(0xc459a2e8, 0x3d2c, 0x4e44, 0xb1, 0x32, 0xfe, 0xe5, 0x15, 0x6c, 0x7b, 0xb0)
	MF_MT_DEFAULT_STRIDE            = guid(0x644b4e48, 0x1e02, 0x4516, 0xb0, 0xeb, 0xc0, 0x1c, 0xa9, 0xd4, 0x9a, 0xc6)
	MF_LOW_LATENCY                  = guid(0x9c27891a, 0xed7a, 0x40e1, 0x88, 0xe8, 0xb2, 0x27, 0x27, 0xa0, 0x24, 0xee)
	MFMediaType_Video               = guid(0x73646976, 0x0000, 0x0010, 0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71)
	MF_READWRITE_DISABLE_CONVERTERS = guid(0x98d5b065, 0x1374, 0x4847, 0x8d, 0x5d, 0x31, 0x52, 0x0f, 0xee, 0x71, 0x56)
	// MFSampleExtension_DeviceTimestamp is the driver's capture time of a
	// sample, in 100 ns units of QueryPerformanceCounter time (Windows SDK
	// mfapi.h; not in mingw-w64's headers).
	MFSampleExtension_DeviceTimestamp = guid(0x8f3e35e7, 0x2dcd, 0x4887, 0x86, 0x22, 0x2a, 0x58, 0xba, 0xa6, 0x52, 0xb0)
)

// VideoFormatGUID returns the MFVideoFormat GUID of a FourCC (e.g. "NV12").
func VideoFormatGUID(fourcc string) GUID {
	b := []byte(fourcc)
	d1 := uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
	return guid(d1, 0x0000, 0x0010, 0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71)
}

// FourCC returns the FourCC of an MFVideoFormat GUID ("" if it is not one).
func FourCC(g GUID) string {
	if g.Data2 != 0 || g.Data3 != 0x0010 {
		return ""
	}
	b := []byte{byte(g.Data1), byte(g.Data1 >> 8), byte(g.Data1 >> 16), byte(g.Data1 >> 24)}
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return ""
		}
	}
	return string(b)
}

// SubtypeName names an MFVideoFormat subtype: its FourCC ("NV12", "YUY2",
// "P010", "UYVY", ...), or "RGB24" / "RGB32" for the D3DFORMAT-based RGB
// subtypes. It returns "" for anything else.
func SubtypeName(g GUID) string {
	if g.Data2 == 0 && g.Data3 == 0x0010 && g.Data4 == [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71} {
		switch g.Data1 {
		case 20: // D3DFMT_R8G8B8
			return "RGB24"
		case 22: // D3DFMT_X8R8G8B8
			return "RGB32"
		}
	}
	return FourCC(g)
}

// vtable indices.
const (
	vtAttrGetUINT32                   = 7
	vtAttrGetUINT64                   = 8
	vtAttrGetGUID                     = 10
	vtAttrGetAllocatedString          = 13
	vtAttrSetUINT32                   = 21
	vtAttrSetGUID                     = 24
	vtActivateActivateObject          = 33
	vtActivateShutdownObject          = 34
	vtSampleGetSampleTime             = 35
	vtSampleConvertToContiguousBuffer = 41
	vtBufferLock                      = 3
	vtBufferUnlock                    = 4
	vt2DLock2D                        = 3
	vt2DUnlock2D                      = 4
	vtReaderSetStreamSelection        = 4
	vtReaderGetNativeMediaType        = 5
	vtReaderSetCurrentMediaType       = 7
	vtReaderReadSample                = 9
	vtSourceShutdown                  = 12
)

// Source reader stream indices and flags.
const (
	MF_SOURCE_READER_FIRST_VIDEO_STREAM = 0xFFFFFFFC
	MF_SOURCE_READER_ALL_STREAMS        = 0xFFFFFFFE
	MF_SOURCE_READERF_ERROR             = 0x1
	MF_SOURCE_READERF_ENDOFSTREAM       = 0x2
	MF_SOURCE_READERF_STREAMTICK        = 0x100
)

// COInitMultithreaded initialises COM on the calling (locked) thread.
func COInitMultithreaded() error {
	r, _, _ := procCoInitializeEx.Call(0, 0)      // COINIT_MULTITHREADED
	if int32(r) < 0 && HRESULT(r) != 0x80010106 { // RPC_E_CHANGED_MODE: already initialised
		return HRESULT(r)
	}
	return nil
}

// COUninit balances COInitMultithreaded.
func COUninit() { procCoUninitialize.Call() }

// MFStartup starts Media Foundation (MF_VERSION, full).
func MFStartup() error {
	r, _, _ := procMFStartup.Call(0x00020070, 0)
	return hr(r)
}

// MFShutdown balances MFStartup.
func MFShutdown() { procMFShutdown.Call() }

// Attributes is IMFAttributes (also the base of media types, samples and
// activation objects).
type Attributes struct{ Object }

// NewAttributes creates an empty attribute store.
func NewAttributes(n uint32) (Attributes, error) {
	var a Object
	r, _, _ := procMFCreateAttributes.Call(uintptr(unsafe.Pointer(&a)), uintptr(n))
	return Attributes{a}, hr(r)
}

// SetGUID sets a GUID attribute.
func (a Attributes) SetGUID(key, value *GUID) error {
	return hr(a.call(vtAttrSetGUID, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(value))))
}

// SetUINT32 sets a UINT32 attribute.
func (a Attributes) SetUINT32(key *GUID, v uint32) error {
	return hr(a.call(vtAttrSetUINT32, uintptr(unsafe.Pointer(key)), uintptr(v)))
}

// GetGUID reads a GUID attribute.
func (a Attributes) GetGUID(key *GUID) (GUID, error) {
	var g GUID
	err := hr(a.call(vtAttrGetGUID, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&g))))
	return g, err
}

// GetUINT32 reads a UINT32 attribute.
func (a Attributes) GetUINT32(key *GUID) (uint32, error) {
	var v uint32
	err := hr(a.call(vtAttrGetUINT32, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&v))))
	return v, err
}

// GetUINT64 reads a UINT64 attribute.
func (a Attributes) GetUINT64(key *GUID) (uint64, error) {
	var v uint64
	err := hr(a.call(vtAttrGetUINT64, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&v))))
	return v, err
}

// GetString reads a string attribute.
func (a Attributes) GetString(key *GUID) (string, error) {
	var p *uint16
	var n uint32
	if err := hr(a.call(vtAttrGetAllocatedString, uintptr(unsafe.Pointer(key)),
		uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&n)))); err != nil {
		return "", err
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return windows.UTF16PtrToString(p), nil
}

// EnumVideoCaptureDevices returns activation objects for the video capture
// devices. Release each one.
func EnumVideoCaptureDevices() ([]Attributes, error) {
	attr, err := NewAttributes(1)
	if err != nil {
		return nil, err
	}
	defer attr.Release()
	if err := attr.SetGUID(&MF_DEVSOURCE_ATTRIBUTE_SOURCE_TYPE, &MF_DEVSOURCE_ATTRIBUTE_SOURCE_TYPE_VIDCAP_GUID); err != nil {
		return nil, err
	}
	var arr uintptr
	var n uint32
	r, _, _ := procMFEnumDeviceSources.Call(uintptr(attr.Object), uintptr(unsafe.Pointer(&arr)), uintptr(unsafe.Pointer(&n)))
	if err := hr(r); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	defer procCoTaskMemFree.Call(arr)
	ptrs := unsafe.Slice(*(**Object)(unsafe.Pointer(&arr)), n)
	out := make([]Attributes, n)
	for i, p := range ptrs {
		out[i] = Attributes{p}
	}
	return out, nil
}

// ActivateMediaSource creates the IMFMediaSource of a device.
func (a Attributes) ActivateMediaSource() (Object, error) {
	var s Object
	err := hr(a.call(vtActivateActivateObject, uintptr(unsafe.Pointer(&IID_IMFMediaSource)), uintptr(unsafe.Pointer(&s))))
	return s, err
}

// ShutdownMediaSource stops a media source (IMFMediaSource::Shutdown).
func ShutdownMediaSource(s Object) { s.call(vtSourceShutdown) }

// SourceReader is IMFSourceReader.
type SourceReader struct{ Object }

// NewSourceReader creates a source reader for a media source.
func NewSourceReader(source Object, attrs Attributes) (SourceReader, error) {
	var r Object
	res, _, _ := procMFCreateSourceReaderFromMediaSource.Call(uintptr(source), uintptr(attrs.Object), uintptr(unsafe.Pointer(&r)))
	return SourceReader{r}, hr(res)
}

// SetStreamSelection selects or deselects a stream.
func (r SourceReader) SetStreamSelection(stream uint32, on bool) error {
	b := uintptr(0)
	if on {
		b = 1
	}
	return hr(r.call(vtReaderSetStreamSelection, uintptr(stream), b))
}

// NativeMediaType returns native media type i of a stream; the error is
// MF_E_NO_MORE_TYPES past the end.
func (r SourceReader) NativeMediaType(stream, i uint32) (Attributes, error) {
	var t Object
	err := hr(r.call(vtReaderGetNativeMediaType, uintptr(stream), uintptr(i), uintptr(unsafe.Pointer(&t))))
	return Attributes{t}, err
}

// SetCurrentMediaType sets the output type of a stream.
func (r SourceReader) SetCurrentMediaType(stream uint32, t Attributes) error {
	return hr(r.call(vtReaderSetCurrentMediaType, uintptr(stream), 0, uintptr(t.Object)))
}

// ReadSample reads the next sample synchronously. The sample may be zero
// (a stream tick or gap).
func (r SourceReader) ReadSample(stream uint32) (sample Attributes, flags uint32, timestamp int64, err error) {
	var actual uint32
	var s Object
	err = hr(r.call(vtReaderReadSample, uintptr(stream), 0, uintptr(unsafe.Pointer(&actual)),
		uintptr(unsafe.Pointer(&flags)), uintptr(unsafe.Pointer(&timestamp)), uintptr(unsafe.Pointer(&s))))
	return Attributes{s}, flags, timestamp, err
}

// SampleBuffer returns a sample's data as one buffer.
func SampleBuffer(sample Attributes) (Object, error) {
	var b Object
	err := hr(sample.call(vtSampleConvertToContiguousBuffer, uintptr(unsafe.Pointer(&b))))
	return b, err
}

// LockBuffer locks a media buffer and returns its bytes and row pitch: a
// 2D buffer reports its pitch; a plain one gets pitch 0 (the caller uses
// the format's default stride). For bottom-up images (IMF2DBuffer reports
// negative pitch), data starts at the lowest address (the bottom row), pitch
// is positive, and bottomUp is true; rows locates that bottom row. Unlock
// with the returned function.
func LockBuffer(buf Object, rows int) (data []byte, pitch int, bottomUp bool, unlock func(), err error) {
	if b2, e := buf.QueryInterface(&IID_IMF2DBuffer); e == nil {
		var scan0 *byte
		var p int32
		if e := hr(b2.call(vt2DLock2D, uintptr(unsafe.Pointer(&scan0)), uintptr(unsafe.Pointer(&p)))); e == nil {
			unlock2D := func() { b2.call(vt2DUnlock2D); b2.Release() }
			if p > 0 {
				// The length is not reported: callers read pitch * rows.
				return unsafe.Slice(scan0, 1<<30), int(p), false, unlock2D, nil
			}
			if p < 0 && rows > 0 {
				base := unsafe.Add(unsafe.Pointer(scan0), int(p)*(rows-1))
				return unsafe.Slice((*byte)(base), int(-p)*rows), int(-p), true, unlock2D, nil
			}
			unlock2D()
		} else {
			b2.Release()
		}
	}
	var ptr *byte
	var max, cur uint32
	if err := hr(buf.call(vtBufferLock, uintptr(unsafe.Pointer(&ptr)), uintptr(unsafe.Pointer(&max)), uintptr(unsafe.Pointer(&cur)))); err != nil {
		return nil, 0, false, nil, err
	}
	return unsafe.Slice(ptr, cur), 0, false, func() { buf.call(vtBufferUnlock) }, nil
}
