package v4l2

import (
	"encoding/binary"
	"errors"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/virgil724/caplat/capture"
	"github.com/virgil724/caplat/internal/barcode"
)

func TestV4L2LayoutsAndIOCTLs(t *testing.T) {
	cases := []struct {
		name      string
		got, want uintptr
	}{
		{"v4l2Capability", unsafe.Sizeof(v4l2Capability{}), 104},
		{"v4l2Capability.Capabilities", unsafe.Offsetof(v4l2Capability{}.Capabilities), 84},
		{"v4l2Capability.DeviceCaps", unsafe.Offsetof(v4l2Capability{}.DeviceCaps), 88},
		{"v4l2FmtDesc", unsafe.Sizeof(v4l2FmtDesc{}), 64},
		{"v4l2FmtDesc.PixelFormat", unsafe.Offsetof(v4l2FmtDesc{}.PixelFormat), 44},
		{"v4l2FrmSizeEnum", unsafe.Sizeof(v4l2FrmSizeEnum{}), 44},
		{"v4l2FrmSizeEnum.Data", unsafe.Offsetof(v4l2FrmSizeEnum{}.Data), 12},
		{"v4l2FrmIvalEnum", unsafe.Sizeof(v4l2FrmIvalEnum{}), 52},
		{"v4l2FrmIvalEnum.Data", unsafe.Offsetof(v4l2FrmIvalEnum{}.Data), 20},
		{"v4l2PixFormat", unsafe.Sizeof(v4l2PixFormat{}), 48},
		{"v4l2Format", unsafe.Sizeof(v4l2Format{}), 208},
		{"v4l2Format.Fmt", unsafe.Offsetof(v4l2Format{}.Fmt), 8},
		{"v4l2StreamParm", unsafe.Sizeof(v4l2StreamParm{}), 204},
		{"v4l2StreamParm.Parm", unsafe.Offsetof(v4l2StreamParm{}.Parm), 4},
		{"v4l2RequestBuffers", unsafe.Sizeof(v4l2RequestBuffers{}), 20},
		{"v4l2Buffer", unsafe.Sizeof(v4l2Buffer{}), 88},
		{"v4l2Buffer.TimestampSec", unsafe.Offsetof(v4l2Buffer{}.TimestampSec), 24},
		{"v4l2Buffer.TimestampUsec", unsafe.Offsetof(v4l2Buffer{}.TimestampUsec), 32},
		{"v4l2Buffer.Sequence", unsafe.Offsetof(v4l2Buffer{}.Sequence), 56},
		{"v4l2Buffer.M", unsafe.Offsetof(v4l2Buffer{}.M), 64},
		{"v4l2Buffer.Length", unsafe.Offsetof(v4l2Buffer{}.Length), 72},
		{"v4l2Buffer.RequestFD", unsafe.Offsetof(v4l2Buffer{}.RequestFD), 80},

		{"VIDIOC_QUERYCAP", vidiocQueryCap, 0x80685600},
		{"VIDIOC_ENUM_FMT", vidiocEnumFmt, 0xC0405602},
		{"VIDIOC_G_FMT", vidiocGFmt, 0xC0D05604},
		{"VIDIOC_S_FMT", vidiocSFmt, 0xC0D05605},
		{"VIDIOC_REQBUFS", vidiocReqBufs, 0xC0145608},
		{"VIDIOC_QUERYBUF", vidiocQueryBuf, 0xC0585609},
		{"VIDIOC_QBUF", vidiocQBuf, 0xC058560F},
		{"VIDIOC_DQBUF", vidiocDQBuf, 0xC0585611},
		{"VIDIOC_STREAMON", vidiocStreamOn, 0x40045612},
		{"VIDIOC_STREAMOFF", vidiocStreamOff, 0x40045613},
		{"VIDIOC_G_PARM", vidiocGParm, 0xC0CC5615},
		{"VIDIOC_S_PARM", vidiocSParm, 0xC0CC5616},
		{"VIDIOC_ENUM_FRAMESIZES", vidiocEnumFrameSizes, 0xC02C564A},
		{"VIDIOC_ENUM_FRAMEINTERVALS", vidiocEnumFrameIntervals, 0xC034564B},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %#x (%d), want %#x (%d)", tc.name, tc.got, tc.got, tc.want, tc.want)
		}
	}
}

type fakeSystem struct {
	nodes map[string]*fakeV4L2Device
	files map[string][]byte
}

func (s *fakeSystem) Glob(pattern string) ([]string, error) {
	var paths []string
	for k := range s.nodes {
		paths = append(paths, k)
	}
	return paths, nil
}

func (s *fakeSystem) Open(path string, _ bool) (v4l2Device, error) {
	d, ok := s.nodes[path]
	if !ok {
		return nil, errors.New("no such device")
	}
	d.mu.Lock()
	d.closed = false
	d.mu.Unlock()
	return d, nil
}

func (s *fakeSystem) ReadFile(path string) ([]byte, error) {
	b, ok := s.files[path]
	if !ok {
		return nil, errors.New("not found")
	}
	return b, nil
}

type fakeFmtSpec struct {
	fourcc string
	w, h   int
	fps    [][2]uint32 // interval num/den pairs
}

type fakeV4L2Device struct {
	mu          sync.Mutex
	card        string
	driver      string
	busInfo     string
	caps        uint32
	devCaps     uint32
	fmts        []fakeFmtSpec
	negotiated  Mode
	pitch       int
	bufLen      int
	bufMem      [][]byte
	queued      []uint32
	ready       []v4l2Buffer
	unmapped    []bool
	closed      bool
	reqBufCount uint32
	streamOn    bool
	wakeCh      chan struct{}

	qbufHistory    []uint32
	minQueued      int
	frameSource    func(seq int, buf []byte) v4l2Buffer
	nextSeq        int
	maxFrames      int
	pauseAtSeq     int
	resumeCh       chan struct{}
	streamOnErrs   []error
	streamOnCalls  int
	streamOffCalls int
	streamOnQueued []int
}

func newFakeCaptureDevice(card string, fmts []fakeFmtSpec) *fakeV4L2Device {
	return &fakeV4L2Device{
		card:      card,
		driver:    "uvcvideo",
		busInfo:   "usb-0000:00:14.0-1",
		caps:      v4l2CapVideoCapture | v4l2CapStreaming | v4l2CapDeviceCaps,
		devCaps:   v4l2CapVideoCapture | v4l2CapStreaming,
		fmts:      fmts,
		wakeCh:    make(chan struct{}, 16),
		minQueued: 1 << 30,
	}
}

func (d *fakeV4L2Device) Wake() {
	select {
	case d.wakeCh <- struct{}{}:
	default:
	}
}

func (d *fakeV4L2Device) Close() error {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	return nil
}

func (d *fakeV4L2Device) Mmap(offset int64, length int) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	idx := int(offset / 0x1000000)
	if idx < 0 || idx >= len(d.bufMem) {
		return nil, errors.New("bad mmap offset")
	}
	return d.bufMem[idx], nil
}

func (d *fakeV4L2Device) Munmap(data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, b := range d.bufMem {
		if len(b) > 0 && len(data) > 0 && &b[0] == &data[0] {
			d.unmapped[i] = true
		}
	}
	return nil
}

func (d *fakeV4L2Device) produceFrameLocked() bool {
	if len(d.queued) < d.minQueued {
		d.minQueued = len(d.queued)
	}
	if len(d.queued) == 0 || (d.maxFrames > 0 && d.nextSeq >= d.maxFrames) {
		return false
	}
	if d.pauseAtSeq > 0 && d.nextSeq >= d.pauseAtSeq {
		select {
		case <-d.resumeCh:
		default:
			return false
		}
	}
	idx := d.queued[0]
	d.queued = d.queued[1:]
	d.nextSeq++
	var vb v4l2Buffer
	if d.frameSource != nil {
		vb = d.frameSource(d.nextSeq, d.bufMem[idx])
	} else {
		vb = v4l2Buffer{
			Flags:         v4l2BufFlagTimestampMonotonic | v4l2BufFlagTstampSrcSOE,
			TimestampSec:  1000,
			TimestampUsec: int64(d.nextSeq) * 1000,
			BytesUsed:     uint32(d.bufLen),
		}
	}
	vb.Index = idx
	vb.Type = v4l2BufTypeVideoCapture
	vb.Memory = v4l2MemoryMMAP
	vb.Sequence = uint32(d.nextSeq)
	d.ready = append(d.ready, vb)
	return true
}

func (d *fakeV4L2Device) Poll(timeoutMS int) (frameReady, woke bool, err error) {
	d.mu.Lock()
	if d.streamOn && len(d.ready) == 0 {
		d.produceFrameLocked()
	}
	if len(d.ready) > 0 {
		d.mu.Unlock()
		return true, false, nil
	}
	d.mu.Unlock()

	select {
	case <-d.wakeCh:
		d.mu.Lock()
		has := len(d.ready) > 0
		d.mu.Unlock()
		return has, true, nil
	case <-time.After(10 * time.Millisecond):
		d.mu.Lock()
		if d.streamOn && len(d.ready) == 0 {
			d.produceFrameLocked()
		}
		has := len(d.ready) > 0
		d.mu.Unlock()
		return has, false, nil
	}
}

func (d *fakeV4L2Device) Ioctl(req uintptr, ptr unsafe.Pointer) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	switch req {
	case vidiocQueryCap:
		c := (*v4l2Capability)(ptr)
		copy(c.Driver[:], d.driver)
		copy(c.Card[:], d.card)
		copy(c.BusInfo[:], d.busInfo)
		c.Capabilities = d.caps
		c.DeviceCaps = d.devCaps
		return nil

	case vidiocEnumFmt:
		f := (*v4l2FmtDesc)(ptr)
		var seen []string
		for _, spec := range d.fmts {
			found := false
			for _, s := range seen {
				if s == spec.fourcc {
					found = true
					break
				}
			}
			if !found {
				seen = append(seen, spec.fourcc)
			}
		}
		if int(f.Index) >= len(seen) {
			return errors.New("EINVAL")
		}
		fcc, _ := stringToFourCC(seen[f.Index])
		f.PixelFormat = fcc
		copy(f.Description[:], seen[f.Index])
		return nil

	case vidiocEnumFrameSizes:
		fs := (*v4l2FrmSizeEnum)(ptr)
		cc := fourccToString(fs.PixelFormat)
		var sizes [][2]int
		for _, spec := range d.fmts {
			if spec.fourcc == cc {
				sizes = append(sizes, [2]int{spec.w, spec.h})
			}
		}
		if int(fs.Index) >= len(sizes) {
			return errors.New("EINVAL")
		}
		fs.Type = v4l2FrmSizeTypeDiscrete
		binary.LittleEndian.PutUint32(fs.Data[0:4], uint32(sizes[fs.Index][0]))
		binary.LittleEndian.PutUint32(fs.Data[4:8], uint32(sizes[fs.Index][1]))
		return nil

	case vidiocEnumFrameIntervals:
		fi := (*v4l2FrmIvalEnum)(ptr)
		cc := fourccToString(fi.PixelFormat)
		var ivals [][2]uint32
		for _, spec := range d.fmts {
			if spec.fourcc == cc && spec.w == int(fi.Width) && spec.h == int(fi.Height) {
				ivals = append(ivals, spec.fps...)
			}
		}
		if int(fi.Index) >= len(ivals) {
			return errors.New("EINVAL")
		}
		fi.Type = v4l2FrmIvalTypeDiscrete
		binary.LittleEndian.PutUint32(fi.Data[0:4], ivals[fi.Index][0])
		binary.LittleEndian.PutUint32(fi.Data[4:8], ivals[fi.Index][1])
		return nil

	case vidiocGFmt:
		return nil

	case vidiocSFmt:
		f := (*v4l2Format)(ptr)
		p := f.pix()
		cc := fourccToString(p.PixelFormat)
		pitch := capture.DefaultPitch(cc, int(p.Width))
		if pitch == 0 {
			return errors.New("EINVAL")
		}
		p.BytesPerLine = uint32(pitch)
		p.SizeImage = uint32(capture.RawSize(cc, pitch, int(p.Height)))
		f.setPix(p)
		d.pitch = pitch
		d.bufLen = int(p.SizeImage)
		d.negotiated.FourCC = cc
		d.negotiated.Width = int(p.Width)
		d.negotiated.Height = int(p.Height)
		return nil

	case vidiocGParm, vidiocSParm:
		sp := (*v4l2StreamParm)(ptr)
		n, den := sp.timePerFrame()
		if n > 0 && den > 0 {
			d.negotiated.FPS = float64(den) / float64(n)
		}
		return nil

	case vidiocReqBufs:
		rb := (*v4l2RequestBuffers)(ptr)
		d.reqBufCount = rb.Count
		if rb.Count == 0 {
			d.queued = nil
			d.ready = nil
			return nil
		}
		d.bufMem = make([][]byte, rb.Count)
		d.unmapped = make([]bool, rb.Count)
		for i := range d.bufMem {
			d.bufMem[i] = make([]byte, d.bufLen)
		}
		return nil

	case vidiocQueryBuf:
		vb := (*v4l2Buffer)(ptr)
		vb.Length = uint32(d.bufLen)
		vb.M = uint64(vb.Index) * 0x1000000
		return nil

	case vidiocQBuf:
		vb := (*v4l2Buffer)(ptr)
		d.queued = append(d.queued, vb.Index)
		d.qbufHistory = append(d.qbufHistory, vb.Index)
		return nil

	case vidiocDQBuf:
		if len(d.ready) == 0 {
			return errors.New("resource temporarily unavailable")
		}
		vb := (*v4l2Buffer)(ptr)
		*vb = d.ready[0]
		d.ready = d.ready[1:]
		return nil

	case vidiocStreamOn:
		d.streamOnCalls++
		d.streamOnQueued = append(d.streamOnQueued, len(d.queued))
		if len(d.streamOnErrs) > 0 {
			err := d.streamOnErrs[0]
			d.streamOnErrs = d.streamOnErrs[1:]
			if err != nil {
				return err
			}
		}
		d.streamOn = true
		return nil

	case vidiocStreamOff:
		d.streamOffCalls++
		d.streamOn = false
		d.queued = nil
		d.ready = nil
		return nil
	}
	return errors.New("unsupported ioctl")
}

func TestListDeviceInfosFiltersMetaNode(t *testing.T) {
	videoNode := newFakeCaptureDevice("Live Gamer Ultra-Video", nil)
	metaNode := &fakeV4L2Device{
		card:    "Live Gamer Ultra-Video",
		driver:  "uvcvideo",
		caps:    v4l2CapMetaCapture | v4l2CapStreaming | v4l2CapDeviceCaps,
		devCaps: v4l2CapMetaCapture | v4l2CapStreaming,
	}
	sys := &fakeSystem{
		nodes: map[string]*fakeV4L2Device{
			"/dev/video0": videoNode,
			"/dev/video1": metaNode,
		},
	}
	infos, err := listDeviceInfosWith(sys)
	if err != nil {
		t.Fatalf("listDeviceInfosWith: %v", err)
	}
	if len(infos) != 1 || infos[0].Path != "/dev/video0" || infos[0].DisplayName() != "Live Gamer Ultra-Video (/dev/video0)" {
		t.Fatalf("got infos=%+v", infos)
	}
	p, err := resolveDevicePath(sys, "Live Gamer Ultra-Video (/dev/video0)")
	if err != nil || p != "/dev/video0" {
		t.Errorf("resolveDevicePath display name = (%q, %v)", p, err)
	}
	p, err = resolveDevicePath(sys, "Live Gamer Ultra")
	if err != nil || p != "/dev/video0" {
		t.Errorf("resolveDevicePath substring = (%q, %v)", p, err)
	}
}

func TestBufDeviceTicksAndTimestamps(t *testing.T) {
	const freq = 1_000_000_000
	arrive := int64(1000*1e9 + 5000*1e3)

	// Normal MONOTONIC + SOE with device <= arrive.
	ticks, src := bufDeviceTicks(v4l2Buffer{
		Flags:         v4l2BufFlagTimestampMonotonic | v4l2BufFlagTstampSrcSOE,
		TimestampSec:  1000,
		TimestampUsec: 2000,
	}, arrive, freq)
	if ticks != 1000*1e9+2000*1e3 || src != "SOE" {
		t.Errorf("got (%d, %q)", ticks, src)
	}

	// Firmware PTS bug where device > arrive must clamp DeviceQPC to 0.
	ticks, src = bufDeviceTicks(v4l2Buffer{
		Flags:         v4l2BufFlagTimestampMonotonic | v4l2BufFlagTstampSrcEOF,
		TimestampSec:  1001,
		TimestampUsec: 0,
	}, arrive, freq)
	if ticks != 0 || src != "EOF" {
		t.Errorf("future timestamp got (%d, %q), want (0, EOF)", ticks, src)
	}

	// Non-monotonic timestamp ignored.
	ticks, src = bufDeviceTicks(v4l2Buffer{
		Flags:         0,
		TimestampSec:  1000,
		TimestampUsec: 1000,
	}, arrive, freq)
	if ticks != 0 || src != "" {
		t.Errorf("non-monotonic got (%d, %q), want (0, '')", ticks, src)
	}
}

func writeBarcodeNV12(buf []byte, w, h int, frame uint32) {
	cells := barcode.Encode(frame)
	cw := w / barcode.Cells
	for y := 0; y < cw; y++ {
		for x := 0; x < w; x++ {
			if cells[x/cw] {
				buf[y*w+x] = 255
			} else {
				buf[y*w+x] = 0
			}
		}
	}
}

func TestHoldReleaseAccountingAndNonStarvation(t *testing.T) {
	const w, h = 320, 180
	dev := newFakeCaptureDevice("AVT GC553", []fakeFmtSpec{
		{fourcc: "NV12", w: w, h: h, fps: [][2]uint32{{1, 60}}},
		{fourcc: "YUYV", w: w, h: h, fps: [][2]uint32{{1, 60}}},
	})
	dev.pauseAtSeq = 4
	dev.resumeCh = make(chan struct{})
	dev.frameSource = func(seq int, buf []byte) v4l2Buffer {
		writeBarcodeNV12(buf, w, h, uint32(seq))
		return v4l2Buffer{
			Flags:         v4l2BufFlagTimestampMonotonic | v4l2BufFlagTstampSrcSOE,
			TimestampSec:  10,
			TimestampUsec: int64(seq) * 1000,
			BytesUsed:     uint32(len(buf)),
		}
	}
	sys := &fakeSystem{
		nodes: map[string]*fakeV4L2Device{"/dev/video0": dev},
		files: map[string][]byte{uvcHWTimestampsPath: []byte("0\n")},
	}

	var mu sync.Mutex
	var held []func()
	var seen []uint32
	var postCallbackFrame capture.Frame
	fourDone := make(chan struct{})
	resumed := make(chan struct{})
	var count int

	clk := int64(10_000_000_000 + 100_000_000)
	src, err := startWith(sys, Config{
		Device:  "/dev/video0",
		Width:   w,
		Height:  h,
		FPS:     60,
		Format:  "auto",
		Buffers: 2, // must clamp up to >= 4
	}, func() int64 { return clk }, 1_000_000_000, func(f capture.Frame) {
		geo := barcode.Geometry{OutW: w, OutH: h, CapW: w, CapH: h}
		code, ok := geo.Decode(f.Y, f.Stride)
		if !ok {
			t.Errorf("failed to decode barcode from zero-copy NV12 frame")
		}
		mu.Lock()
		count++
		n := count
		seen = append(seen, code)
		mu.Unlock()

		if n <= 3 {
			rel := f.Hold()
			if rel == nil {
				t.Errorf("frame %d Hold() returned nil unexpectedly", n)
			} else {
				mu.Lock()
				held = append(held, rel)
				mu.Unlock()
			}
			postCallbackFrame = f
		} else if n == 4 {
			// 3 of 4 buffers are held: 4th Hold() MUST return nil so driver never starves.
			if rel := f.Hold(); rel != nil {
				t.Errorf("frame 4 Hold() succeeded despite saturated buffer pool")
				rel()
			}
			close(fourDone)
		} else if n == 5 {
			rel := f.Hold()
			if rel == nil {
				t.Errorf("frame 5 Hold() after release returned nil")
			} else {
				mu.Lock()
				held = append(held, rel)
				mu.Unlock()
			}
			select {
			case <-resumed:
			default:
				close(resumed)
			}
		}
	})
	if err != nil {
		t.Fatalf("startWith: %v", err)
	}
	if dev.reqBufCount < 4 {
		t.Fatalf("REQBUFS count = %d, want >= 4", dev.reqBufCount)
	}
	if src.Mode.FourCC != "NV12" {
		t.Errorf("negotiated FourCC = %q, want NV12", src.Mode.FourCC)
	}

	<-fourDone
	// Wait briefly for frame 4's immediate QBUF so qBefore includes frame 4.
	deadline := time.Now().Add(time.Second)
	for {
		dev.mu.Lock()
		qNow := len(dev.qbufHistory)
		dev.mu.Unlock()
		if qNow >= 5 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if postCallbackFrame.Hold() != nil {
		t.Errorf("Hold() after callback returned should be nil")
	}
	if src.Dropped() != 1 {
		t.Errorf("Dropped() = %d, want 1", src.Dropped())
	}

	// Release oldest held frame twice (idempotent check); driver should immediately receive QBUF and deliver frame 5.
	mu.Lock()
	oldest := held[0]
	held = held[1:]
	mu.Unlock()
	dev.mu.Lock()
	qBefore := len(dev.qbufHistory)
	dev.mu.Unlock()

	oldest()
	oldest() // idempotent second call

	// Wait for drainRequeue to QBUF the released buffer before allowing frame 5.
	var qAfter int
	deadline = time.Now().Add(time.Second)
	for {
		dev.mu.Lock()
		qAfter = len(dev.qbufHistory)
		dev.mu.Unlock()
		if qAfter > qBefore || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if qAfter <= qBefore {
		t.Fatalf("expected release() to trigger VIDIOC_QBUF (before=%d after=%d)", qBefore, qAfter)
	}

	close(dev.resumeCh)
	dev.Wake()

	<-resumed
	mu.Lock()
	stillHeld := make([]func(), len(held))
	copy(stillHeld, held)
	mu.Unlock()
	dev.mu.Lock()
	minQ := dev.minQueued
	dev.mu.Unlock()
	if minQ < 1 {
		t.Errorf("driver queue starved! minQueued=%d", minQ)
	}

	// Stop capture while buffers are STILL held! Verify held buffers stay mapped until released.
	src.Stop()
	if err := <-src.Done(); err != nil {
		t.Fatalf("Done() = %v", err)
	}
	if dev.closed {
		t.Errorf("device closed prematurely while %d frames were still held", len(stillHeld))
	}
	for _, rel := range stillHeld {
		rel()
	}
	if !dev.closed {
		t.Errorf("device was not closed after releasing all held frames")
	}
	for i, u := range dev.unmapped {
		if !u {
			t.Errorf("buffer %d was not unmapped after final release", i)
		}
	}
	mu.Lock()
	gotCount := len(seen)
	mu.Unlock()
	if gotCount < 5 {
		t.Errorf("captured %d frames, want >= 5", gotCount)
	}
	if ts := src.TimestampInfo(); ts.Source != "SOE" || ts.HWTimestamps != "0" {
		t.Errorf("TimestampInfo = %+v", ts)
	}
}

func TestStreamOnRetryOnENOENT(t *testing.T) {
	const w, h = 320, 180
	newDev := func(errs ...error) (*fakeV4L2Device, *fakeSystem) {
		dev := newFakeCaptureDevice("AVT GC553", []fakeFmtSpec{
			{fourcc: "NV12", w: w, h: h, fps: [][2]uint32{{1, 60}}},
		})
		dev.streamOnErrs = errs
		dev.frameSource = func(seq int, buf []byte) v4l2Buffer {
			writeBarcodeNV12(buf, w, h, uint32(seq))
			return v4l2Buffer{
				Flags:         v4l2BufFlagTimestampMonotonic | v4l2BufFlagTstampSrcSOE,
				TimestampSec:  10,
				TimestampUsec: int64(seq) * 1000,
				BytesUsed:     uint32(len(buf)),
			}
		}
		sys := &fakeSystem{
			nodes: map[string]*fakeV4L2Device{"/dev/video0": dev},
		}
		return dev, sys
	}

	t.Run("succeeds_on_retry", func(t *testing.T) {
		dev, sys := newDev(syscall.ENOENT, nil)
		gotFrame := make(chan struct{}, 4)
		clk := int64(10_100_000_000)
		src, err := startWith(sys, Config{
			Device: "/dev/video0", Width: w, Height: h, FPS: 60, Format: "auto", Buffers: 4,
		}, func() int64 { return clk }, 1_000_000_000, func(capture.Frame) {
			select {
			case gotFrame <- struct{}{}:
			default:
			}
		})
		if err != nil {
			t.Fatalf("startWith: %v", err)
		}
		select {
		case <-gotFrame:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for frame after STREAMON retry")
		}
		dev.mu.Lock()
		onCalls := dev.streamOnCalls
		offCalls := dev.streamOffCalls
		queuedPerOn := append([]int(nil), dev.streamOnQueued...)
		dev.mu.Unlock()
		if onCalls != 2 || offCalls != 1 {
			t.Errorf("streamOnCalls=%d streamOffCalls=%d, want 2/1", onCalls, offCalls)
		}
		if len(queuedPerOn) != 2 || queuedPerOn[0] != 4 || queuedPerOn[1] != 4 {
			t.Errorf("queued buffers at STREAMON = %v, want [4 4]", queuedPerOn)
		}
		src.Stop()
		if err := <-src.Done(); err != nil {
			t.Fatalf("Done() = %v", err)
		}
	})

	t.Run("no_retry_on_other_error", func(t *testing.T) {
		dev, sys := newDev(syscall.EBUSY)
		_, err := startWith(sys, Config{
			Device: "/dev/video0", Width: w, Height: h, FPS: 60, Format: "auto", Buffers: 4,
		}, func() int64 { return 1000 }, 1_000_000_000, func(capture.Frame) {})
		if err == nil {
			t.Fatal("expected error on EBUSY")
		}
		dev.mu.Lock()
		onCalls := dev.streamOnCalls
		offCalls := dev.streamOffCalls
		closed := dev.closed
		unmapped := append([]bool(nil), dev.unmapped...)
		dev.mu.Unlock()
		if onCalls != 1 || offCalls != 0 {
			t.Errorf("streamOnCalls=%d streamOffCalls=%d, want 1/0", onCalls, offCalls)
		}
		if !closed {
			t.Errorf("device not closed after STREAMON failure")
		}
		for i, u := range unmapped {
			if !u {
				t.Errorf("buffer %d not unmapped after STREAMON failure", i)
			}
		}
	})

	t.Run("fails_if_retry_also_enoent", func(t *testing.T) {
		dev, sys := newDev(syscall.ENOENT, syscall.ENOENT)
		_, err := startWith(sys, Config{
			Device: "/dev/video0", Width: w, Height: h, FPS: 60, Format: "auto", Buffers: 4,
		}, func() int64 { return 1000 }, 1_000_000_000, func(capture.Frame) {})
		if err == nil {
			t.Fatal("expected error when retry also returns ENOENT")
		}
		dev.mu.Lock()
		onCalls := dev.streamOnCalls
		offCalls := dev.streamOffCalls
		closed := dev.closed
		unmapped := append([]bool(nil), dev.unmapped...)
		dev.mu.Unlock()
		if onCalls != 2 || offCalls != 1 {
			t.Errorf("streamOnCalls=%d streamOffCalls=%d, want 2/1", onCalls, offCalls)
		}
		if !closed {
			t.Errorf("device not closed after STREAMON retry failure")
		}
		for i, u := range unmapped {
			if !u {
				t.Errorf("buffer %d not unmapped after STREAMON retry failure", i)
			}
		}
	})
}

func TestListModesVsListAllModes(t *testing.T) {
	dev := newFakeCaptureDevice("AVT GC553", []fakeFmtSpec{
		{fourcc: "NV12", w: 1920, h: 1080, fps: [][2]uint32{{1, 60}, {1, 30}}},
		{fourcc: "YUYV", w: 1280, h: 720, fps: [][2]uint32{{1, 60}}},
		{fourcc: "MJPG", w: 3840, h: 2160, fps: [][2]uint32{{1, 60}, {1, 30}}},
	})
	capturable, err := listModesWith(dev, false)
	if err != nil {
		t.Fatalf("listModesWith(false): %v", err)
	}
	if len(capturable) != 3 {
		t.Fatalf("capturable len = %d, want 3: %+v", len(capturable), capturable)
	}
	for _, m := range capturable {
		if m.FourCC == "MJPG" {
			t.Errorf("listModesWith(false) should not include non-capturable MJPG: %+v", m)
		}
	}
	all, err := listModesWith(dev, true)
	if err != nil {
		t.Fatalf("listModesWith(true): %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("all len = %d, want 5: %+v", len(all), all)
	}
	var mjpgFPS []float64
	for _, m := range all {
		if m.FourCC == "MJPG" && m.Width == 3840 && m.Height == 2160 {
			mjpgFPS = append(mjpgFPS, m.FPS)
		}
	}
	if len(mjpgFPS) != 2 || mjpgFPS[0] != 60 || mjpgFPS[1] != 30 {
		t.Errorf("MJPG FPS = %v, want [60 30]", mjpgFPS)
	}
}
