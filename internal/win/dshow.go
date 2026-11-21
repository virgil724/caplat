//go:build windows

package win

import (
	"encoding/binary"
	"errors"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DirectShow: just enough to build capture -> SampleGrabber -> NullRenderer
// and receive every sample in a callback. Vtable indices come from the
// method order in strmif.h, control.h and qedit.h (mingw-w64).

var (
	modOleAut32          = windows.NewLazySystemDLL("oleaut32.dll")
	procCoCreateInstance = modOle32.NewProc("CoCreateInstance")
	procVariantClear     = modOleAut32.NewProc("VariantClear")
)

// DirectShow GUIDs.
var (
	CLSID_SystemDeviceEnum         = guid(0x62be5d10, 0x60eb, 0x11d0, 0xbd, 0x3b, 0x00, 0xa0, 0xc9, 0x11, 0xce, 0x86)
	CLSID_VideoInputDeviceCategory = guid(0x860bb310, 0x5d01, 0x11d0, 0xbd, 0x3b, 0x00, 0xa0, 0xc9, 0x11, 0xce, 0x86)
	CLSID_FilterGraph              = guid(0xe436ebb3, 0x524f, 0x11ce, 0x9f, 0x53, 0x00, 0x20, 0xaf, 0x0b, 0xa7, 0x70)
	CLSID_CaptureGraphBuilder2     = guid(0xbf87b6e1, 0x8c27, 0x11d0, 0xb3, 0xf0, 0x00, 0xaa, 0x00, 0x37, 0x61, 0xc5)
	CLSID_SampleGrabber            = guid(0xc1f400a0, 0x3f08, 0x11d3, 0x9f, 0x0b, 0x00, 0x60, 0x08, 0x03, 0x9e, 0x37)
	CLSID_NullRenderer             = guid(0xc1f400a4, 0x3f08, 0x11d3, 0x9f, 0x0b, 0x00, 0x60, 0x08, 0x03, 0x9e, 0x37)
	IID_IUnknown                   = guid(0x00000000, 0x0000, 0x0000, 0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46)
	IID_ICreateDevEnum             = guid(0x29840822, 0x5b84, 0x11d0, 0xbd, 0x3b, 0x00, 0xa0, 0xc9, 0x11, 0xce, 0x86)
	IID_IPropertyBag               = guid(0x55272a00, 0x42cb, 0x11ce, 0x81, 0x35, 0x00, 0xaa, 0x00, 0x4b, 0xb8, 0x51)
	IID_IBaseFilter                = guid(0x56a86895, 0x0ad4, 0x11ce, 0xb0, 0x3a, 0x00, 0x20, 0xaf, 0x0b, 0xa7, 0x70)
	IID_IGraphBuilder              = guid(0x56a868a9, 0x0ad4, 0x11ce, 0xb0, 0x3a, 0x00, 0x20, 0xaf, 0x0b, 0xa7, 0x70)
	IID_ICaptureGraphBuilder2      = guid(0x93e5a4e0, 0x2d50, 0x11d2, 0xab, 0xfa, 0x00, 0xa0, 0xc9, 0xc6, 0xe3, 0x8d)
	IID_IMediaControl              = guid(0x56a868b1, 0x0ad4, 0x11ce, 0xb0, 0x3a, 0x00, 0x20, 0xaf, 0x0b, 0xa7, 0x70)
	IID_IMediaFilter               = guid(0x56a86899, 0x0ad4, 0x11ce, 0xb0, 0x3a, 0x00, 0x20, 0xaf, 0x0b, 0xa7, 0x70)
	IID_IAMStreamConfig            = guid(0xc6e13340, 0x30ac, 0x11d0, 0xa1, 0x8c, 0x00, 0xa0, 0xc9, 0x11, 0x89, 0x56)
	IID_ISampleGrabber             = guid(0x6b652fff, 0x11fe, 0x4fce, 0x92, 0xad, 0x02, 0x66, 0xb5, 0xd7, 0xc7, 0x8f)
	IID_ISampleGrabberCB           = guid(0x0579154a, 0x2b53, 0x4994, 0xb0, 0xd0, 0xe7, 0x73, 0x14, 0x8e, 0xff, 0x85)
	PIN_CATEGORY_CAPTURE           = guid(0xfb6c4281, 0x0353, 0x11d1, 0x90, 0x5f, 0x00, 0x00, 0xc0, 0xcc, 0x16, 0xba)
	MEDIATYPE_Video                = MFMediaType_Video
	MEDIASUBTYPE_RGB24             = guid(0xe436eb7d, 0x524f, 0x11ce, 0x9f, 0x53, 0x00, 0x20, 0xaf, 0x0b, 0xa7, 0x70)
	MEDIASUBTYPE_RGB32             = guid(0xe436eb7e, 0x524f, 0x11ce, 0x9f, 0x53, 0x00, 0x20, 0xaf, 0x0b, 0xa7, 0x70)
	FORMAT_VideoInfo               = guid(0x05589f80, 0xc356, 0x11ce, 0xbf, 0x01, 0x00, 0xaa, 0x00, 0x55, 0x59, 0x5a)
	FORMAT_VideoInfo2              = guid(0xf72a76a0, 0xeb0a, 0x11d0, 0xac, 0xe4, 0x00, 0x00, 0xc0, 0xcc, 0x16, 0xba)
	errNoVideoInfo                 = errors.New("media type has no VIDEOINFOHEADER")
)

// vtable indices.
const (
	vtCreateClassEnumerator = 3  // ICreateDevEnum
	vtEnumNext              = 3  // IEnumMoniker
	vtBindToObject          = 8  // IMoniker (after IUnknown, IPersist, IPersistStream)
	vtBindToStorage         = 9  // IMoniker
	vtPropBagRead           = 3  // IPropertyBag
	vtAddFilter             = 3  // IFilterGraph
	vtSetFiltergraph        = 3  // ICaptureGraphBuilder2
	vtFindInterface         = 6  // ICaptureGraphBuilder2
	vtRenderStream          = 7  // ICaptureGraphBuilder2
	vtSetFormat             = 3  // IAMStreamConfig
	vtGetNumberOfCaps       = 5  // IAMStreamConfig
	vtGetStreamCaps         = 6  // IAMStreamConfig
	vtSetOneShot            = 3  // ISampleGrabber
	vtSGSetMediaType        = 4  // ISampleGrabber
	vtGetConnectedMediaType = 5  // ISampleGrabber
	vtSetBufferSamples      = 6  // ISampleGrabber
	vtSetCallback           = 9  // ISampleGrabber
	vtSetSyncSource         = 8  // IMediaFilter (after IUnknown, IPersist, Stop/Pause/Run/GetState)
	vtControlRun            = 7  // IMediaControl (after IUnknown, IDispatch)
	vtControlStop           = 9  // IMediaControl
	vtSampleGetPointer      = 3  // IMediaSample
	vtSampleActualLength    = 11 // IMediaSample
)

// CoCreate creates an in-process COM object.
func CoCreate(clsid, iid *GUID) (Object, error) {
	var o Object
	r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(clsid)), 0, 1, // CLSCTX_INPROC_SERVER
		uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&o)))
	if err := hr(r); err != nil {
		return 0, err
	}
	return o, nil
}

// AMMediaType is AM_MEDIA_TYPE (88 bytes on 64-bit).
type AMMediaType struct {
	MajorType           GUID
	SubType             GUID
	FixedSizeSamples    int32
	TemporalCompression int32
	SampleSize          uint32
	FormatType          GUID
	Unk                 uintptr
	CbFormat            uint32
	PbFormat            *byte
}

// format returns the format block (COM memory, valid until Free).
func (m *AMMediaType) format() []byte {
	if m.PbFormat == nil || m.CbFormat == 0 {
		return nil
	}
	return unsafe.Slice(m.PbFormat, m.CbFormat)
}

// bmiOffset is where BITMAPINFOHEADER starts in the format block.
func (m *AMMediaType) bmiOffset() (int, error) {
	switch {
	case m.FormatType == FORMAT_VideoInfo && m.CbFormat >= 88:
		return 48, nil
	case m.FormatType == FORMAT_VideoInfo2 && m.CbFormat >= 112:
		return 72, nil
	}
	return 0, errNoVideoInfo
}

// Video returns the size and the frame interval (100 ns) of a video type.
func (m *AMMediaType) Video() (w, h int, interval int64, err error) {
	off, err := m.bmiOffset()
	if err != nil {
		return 0, 0, 0, err
	}
	f := m.format()
	interval = int64(binary.LittleEndian.Uint64(f[40:]))
	w = int(int32(binary.LittleEndian.Uint32(f[off+4:])))
	h = int(int32(binary.LittleEndian.Uint32(f[off+8:])))
	if h < 0 {
		h = -h
	}
	return w, h, interval, nil
}

// DShowSubtypeName names a DirectShow video subtype GUID: "RGB24" / "RGB32"
// for MEDIASUBTYPE_RGB24 / MEDIASUBTYPE_RGB32, or the FourCC / MF subtype name.
func DShowSubtypeName(g GUID) string {
	switch g {
	case MEDIASUBTYPE_RGB24:
		return "RGB24"
	case MEDIASUBTYPE_RGB32:
		return "RGB32"
	}
	return SubtypeName(g)
}

// DShowSubtypeGUID returns the DirectShow subtype GUID for name ("RGB24",
// "RGB32", or a 4-character FourCC like "NV12", "P010", "YUY2").
func DShowSubtypeGUID(name string) GUID {
	switch name {
	case "RGB24":
		return MEDIASUBTYPE_RGB24
	case "RGB32":
		return MEDIASUBTYPE_RGB32
	}
	return VideoFormatGUID(name)
}

// BottomUp reports whether an uncompressed RGB media type is stored bottom-up
// (positive BITMAPINFOHEADER.biHeight in DirectShow).
func (m *AMMediaType) BottomUp() bool {
	if m.SubType != MEDIASUBTYPE_RGB24 && m.SubType != MEDIASUBTYPE_RGB32 {
		return false
	}
	off, err := m.bmiOffset()
	if err != nil {
		return false
	}
	f := m.format()
	return int32(binary.LittleEndian.Uint32(f[off+8:])) > 0
}

// SetInterval sets AvgTimePerFrame (100 ns), at offset 40 in both
// VIDEOINFOHEADER and VIDEOINFOHEADER2.
func (m *AMMediaType) SetInterval(interval int64) {
	if f := m.format(); len(f) >= 48 {
		binary.LittleEndian.PutUint64(f[40:], uint64(interval))
	}
}

// FreeFormat releases the format block and pUnk (FreeMediaType).
func (m *AMMediaType) FreeFormat() {
	if m.PbFormat != nil {
		procCoTaskMemFree.Call(uintptr(unsafe.Pointer(m.PbFormat)))
		m.PbFormat, m.CbFormat = nil, 0
	}
	if m.Unk != 0 {
		Object(m.Unk).Release()
		m.Unk = 0
	}
}

// DeleteMediaType frees a type returned by IAMStreamConfig::GetStreamCaps.
func DeleteMediaType(m *AMMediaType) {
	if m != nil {
		m.FreeFormat()
		procCoTaskMemFree.Call(uintptr(unsafe.Pointer(m)))
	}
}

// VideoStreamConfigCaps is VIDEO_STREAM_CONFIG_CAPS (128 bytes); only the
// frame interval limits are used.
type VideoStreamConfigCaps struct {
	_                [104]byte
	MinFrameInterval int64
	MaxFrameInterval int64
	_                [8]byte
}

// variant is a VARIANT holding a BSTR (24 bytes on 64-bit).
type variant struct {
	VT   uint16
	_    [3]uint16
	BSTR *uint16
	_    uintptr
}

// DShowDevice is a DirectShow video capture device.
type DShowDevice struct {
	Name    string
	Moniker Object
}

// EnumDShowVideoDevices lists the video input devices; Release each
// Moniker.
func EnumDShowVideoDevices() ([]DShowDevice, error) {
	de, err := CoCreate(&CLSID_SystemDeviceEnum, &IID_ICreateDevEnum)
	if err != nil {
		return nil, err
	}
	defer de.Release()
	var em Object
	r := de.call(vtCreateClassEnumerator, uintptr(unsafe.Pointer(&CLSID_VideoInputDeviceCategory)), uintptr(unsafe.Pointer(&em)), 0)
	if err := hr(r); err != nil {
		return nil, err
	}
	if em == 0 { // S_FALSE: the category is empty
		return nil, nil
	}
	defer em.Release()
	var out []DShowDevice
	for {
		var m Object
		var n uint32
		if r := em.call(vtEnumNext, 1, uintptr(unsafe.Pointer(&m)), uintptr(unsafe.Pointer(&n))); r != 0 || n == 0 {
			break
		}
		out = append(out, DShowDevice{Name: friendlyName(m), Moniker: m})
	}
	return out, nil
}

func friendlyName(m Object) string {
	var bag Object
	if hr(m.call(vtBindToStorage, 0, 0, uintptr(unsafe.Pointer(&IID_IPropertyBag)), uintptr(unsafe.Pointer(&bag)))) != nil {
		return ""
	}
	defer bag.Release()
	prop, _ := windows.UTF16PtrFromString("FriendlyName")
	var v variant
	if hr(bag.call(vtPropBagRead, uintptr(unsafe.Pointer(prop)), uintptr(unsafe.Pointer(&v)), 0)) != nil {
		return ""
	}
	defer procVariantClear.Call(uintptr(unsafe.Pointer(&v)))
	if v.VT != 8 || v.BSTR == nil { // VT_BSTR
		return ""
	}
	return windows.UTF16PtrToString(v.BSTR)
}

// BindFilter creates the device's capture filter (IBaseFilter).
func BindFilter(moniker Object) (Object, error) {
	var f Object
	if err := hr(moniker.call(vtBindToObject, 0, 0, uintptr(unsafe.Pointer(&IID_IBaseFilter)), uintptr(unsafe.Pointer(&f)))); err != nil {
		return 0, err
	}
	return f, nil
}

// AddFilter adds a filter to a graph (IFilterGraph::AddFilter).
func AddFilter(graph, filter Object, name string) error {
	n, _ := windows.UTF16PtrFromString(name)
	return hr(graph.call(vtAddFilter, uintptr(filter), uintptr(unsafe.Pointer(n))))
}

// SetFiltergraph attaches a capture graph builder to a graph.
func SetFiltergraph(cgb, graph Object) error {
	return hr(cgb.call(vtSetFiltergraph, uintptr(graph)))
}

// StreamConfig returns the capture pin's IAMStreamConfig.
func StreamConfig(cgb, src Object) (Object, error) {
	var sc Object
	err := hr(cgb.call(vtFindInterface, uintptr(unsafe.Pointer(&PIN_CATEGORY_CAPTURE)), uintptr(unsafe.Pointer(&MEDIATYPE_Video)),
		uintptr(src), uintptr(unsafe.Pointer(&IID_IAMStreamConfig)), uintptr(unsafe.Pointer(&sc))))
	return sc, err
}

// RenderStream connects src's capture pin through mid to sink.
func RenderStream(cgb, src, mid, sink Object) error {
	return hr(cgb.call(vtRenderStream, uintptr(unsafe.Pointer(&PIN_CATEGORY_CAPTURE)), uintptr(unsafe.Pointer(&MEDIATYPE_Video)),
		uintptr(src), uintptr(mid), uintptr(sink)))
}

// StreamCaps returns the number of capabilities of a stream config.
func StreamCaps(sc Object) (int, error) {
	var n, size int32
	if err := hr(sc.call(vtGetNumberOfCaps, uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&size)))); err != nil {
		return 0, err
	}
	if size < int32(unsafe.Sizeof(VideoStreamConfigCaps{})) {
		return 0, errors.New("not a video stream config")
	}
	return int(n), nil
}

// StreamCap returns capability i; free the type with DeleteMediaType.
func StreamCap(sc Object, i int) (*AMMediaType, VideoStreamConfigCaps, error) {
	var mt *AMMediaType
	var caps VideoStreamConfigCaps
	err := hr(sc.call(vtGetStreamCaps, uintptr(i), uintptr(unsafe.Pointer(&mt)), uintptr(unsafe.Pointer(&caps))))
	return mt, caps, err
}

// SetStreamFormat sets the capture pin's format.
func SetStreamFormat(sc Object, mt *AMMediaType) error {
	return hr(sc.call(vtSetFormat, uintptr(unsafe.Pointer(mt))))
}

// SampleGrabber configures a SampleGrabber: accept only the given video
// subtype, keep no copy of samples, and call cb's SampleCB for each one.
func SampleGrabber(sg Object, subtype GUID, cb *GrabberCallback) error {
	mt := AMMediaType{MajorType: MEDIATYPE_Video, SubType: subtype}
	if err := hr(sg.call(vtSGSetMediaType, uintptr(unsafe.Pointer(&mt)))); err != nil {
		return err
	}
	sg.call(vtSetOneShot, 0)
	sg.call(vtSetBufferSamples, 0)
	return hr(sg.call(vtSetCallback, uintptr(unsafe.Pointer(cb)), 0)) // 0: SampleCB
}

// ConnectedType returns the type the SampleGrabber connected with; call
// FreeFormat on it.
func ConnectedType(sg Object) (AMMediaType, error) {
	var mt AMMediaType
	err := hr(sg.call(vtGetConnectedMediaType, uintptr(unsafe.Pointer(&mt))))
	return mt, err
}

// RemoveClock runs the graph without a reference clock, so no renderer
// waits for a sample's presentation time (IMediaFilter::SetSyncSource(NULL)).
func RemoveClock(graph Object) error {
	mf, err := graph.QueryInterface(&IID_IMediaFilter)
	if err != nil {
		return err
	}
	defer mf.Release()
	return hr(mf.call(vtSetSyncSource, 0))
}

// RunGraph and StopGraph drive IMediaControl.
func RunGraph(control Object) error  { return hr(control.call(vtControlRun)) }
func StopGraph(control Object) error { return hr(control.call(vtControlStop)) }

// SampleData returns an IMediaSample's data (valid during the callback).
func SampleData(sample Object) []byte {
	var p *byte
	if hr(sample.call(vtSampleGetPointer, uintptr(unsafe.Pointer(&p)))) != nil || p == nil {
		return nil
	}
	n := int32(sample.call(vtSampleActualLength))
	if n <= 0 {
		return nil
	}
	return unsafe.Slice(p, n)
}

// GrabberCallback is a static ISampleGrabberCB COM object implemented in
// Go. Its first word points at a vtable of syscall.NewCallback thunks;
// Go's GC does not move objects, and callers keep it reachable while the
// graph may call it.
type GrabberCallback struct {
	vtbl    *[5]uintptr
	OnFrame func(sample Object) // runs on DirectShow's streaming thread
}

var (
	grabberVtbl     [5]uintptr
	grabberVtblOnce sync.Once
)

// NewGrabberCallback returns a callback object that calls onFrame.
func NewGrabberCallback(onFrame func(sample Object)) *GrabberCallback {
	grabberVtblOnce.Do(func() {
		grabberVtbl = [5]uintptr{
			syscall.NewCallback(grabberQueryInterface),
			syscall.NewCallback(grabberAddRef),
			syscall.NewCallback(grabberRelease),
			syscall.NewCallback(grabberSampleCB),
			syscall.NewCallback(grabberBufferCB),
		}
	})
	return &GrabberCallback{vtbl: &grabberVtbl, OnFrame: onFrame}
}

func grabberQueryInterface(this, riid, ppv uintptr) uintptr {
	iid := *(**GUID)(unsafe.Pointer(&riid))
	out := *(**uintptr)(unsafe.Pointer(&ppv))
	if *iid == IID_IUnknown || *iid == IID_ISampleGrabberCB {
		*out = this
		return 0
	}
	*out = 0
	return 0x80004002 // E_NOINTERFACE
}

// The object is owned by Go, so reference counts do not matter.
func grabberAddRef(this uintptr) uintptr  { return 2 }
func grabberRelease(this uintptr) uintptr { return 1 }

// grabberSampleCB is SampleCB(this, double SampleTime, IMediaSample*). The
// double travels in a floating-point register: on amd64 it still takes the
// second argument slot, so the sample is the third; on arm64 integer and
// floating-point registers are counted separately, so it is the second.
func grabberSampleCB(this, a1, a2 uintptr) uintptr {
	sample := a2
	if runtime.GOARCH == "arm64" {
		sample = a1
	}
	cb := *(**GrabberCallback)(unsafe.Pointer(&this))
	if cb.OnFrame != nil {
		cb.OnFrame(Object(sample))
	}
	return 0
}

func grabberBufferCB(this, a1, a2, a3 uintptr) uintptr { return 0 }
