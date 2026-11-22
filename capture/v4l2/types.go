package v4l2

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/virgil724/caplat/capture"
)

// ErrUnsupported is returned on non-Linux operating systems.
var ErrUnsupported = errors.New("v4l2: only supported on Linux")

// V4L2 constants from <linux/videodev2.h> for LP64 (amd64 / arm64).
const (
	v4l2CapVideoCapture = 0x00000001
	v4l2CapMetaCapture  = 0x00800000
	v4l2CapStreaming    = 0x04000000
	v4l2CapDeviceCaps   = 0x80000000

	v4l2BufTypeVideoCapture = 1
	v4l2MemoryMMAP          = 1
	v4l2FieldAny            = 0

	v4l2FrmSizeTypeDiscrete   = 1
	v4l2FrmSizeTypeContinuous = 2
	v4l2FrmSizeTypeStepwise   = 3

	v4l2FrmIvalTypeDiscrete   = 1
	v4l2FrmIvalTypeContinuous = 2
	v4l2FrmIvalTypeStepwise   = 3

	v4l2BufFlagError              = 0x00000040
	v4l2BufFlagTimestampMask      = 0x0000e000
	v4l2BufFlagTimestampMonotonic = 0x00002000
	v4l2BufFlagTstampSrcMask      = 0x00070000
	v4l2BufFlagTstampSrcEOF       = 0x00000000
	v4l2BufFlagTstampSrcSOE       = 0x00010000

	vidiocQueryCap           = 0x80685600
	vidiocEnumFmt            = 0xC0405602
	vidiocGFmt               = 0xC0D05604
	vidiocSFmt               = 0xC0D05605
	vidiocReqBufs            = 0xC0145608
	vidiocQueryBuf           = 0xC0585609
	vidiocQBuf               = 0xC058560F
	vidiocDQBuf              = 0xC0585611
	vidiocStreamOn           = 0x40045612
	vidiocStreamOff          = 0x40045613
	vidiocGParm              = 0xC0CC5615
	vidiocSParm              = 0xC0CC5616
	vidiocEnumFrameSizes     = 0xC02C564A
	vidiocEnumFrameIntervals = 0xC034564B

	uvcHWTimestampsPath = "/sys/module/uvcvideo/parameters/hwtimestamps"
)

type v4l2Capability struct {
	Driver       [16]byte
	Card         [32]byte
	BusInfo      [32]byte
	Version      uint32
	Capabilities uint32
	DeviceCaps   uint32
	Reserved     [3]uint32
}

type v4l2FmtDesc struct {
	Index       uint32
	Type        uint32
	Flags       uint32
	Description [32]byte
	PixelFormat uint32
	MBusCode    uint32
	Reserved    [3]uint32
}

type v4l2FrmSizeEnum struct {
	Index       uint32
	PixelFormat uint32
	Type        uint32
	Data        [24]byte
	Reserved    [2]uint32
}

type v4l2FrmIvalEnum struct {
	Index       uint32
	PixelFormat uint32
	Width       uint32
	Height      uint32
	Type        uint32
	Data        [24]byte
	Reserved    [2]uint32
}

type v4l2PixFormat struct {
	Width        uint32
	Height       uint32
	PixelFormat  uint32
	Field        uint32
	BytesPerLine uint32
	SizeImage    uint32
	Colorspace   uint32
	Priv         uint32
	Flags        uint32
	YCbCrEnc     uint32
	Quantization uint32
	XferFunc     uint32
}

type v4l2Format struct {
	Type uint32
	_    uint32
	Fmt  [200]byte
}

func (f *v4l2Format) pix() v4l2PixFormat {
	return v4l2PixFormat{
		Width:        binary.LittleEndian.Uint32(f.Fmt[0:4]),
		Height:       binary.LittleEndian.Uint32(f.Fmt[4:8]),
		PixelFormat:  binary.LittleEndian.Uint32(f.Fmt[8:12]),
		Field:        binary.LittleEndian.Uint32(f.Fmt[12:16]),
		BytesPerLine: binary.LittleEndian.Uint32(f.Fmt[16:20]),
		SizeImage:    binary.LittleEndian.Uint32(f.Fmt[20:24]),
		Colorspace:   binary.LittleEndian.Uint32(f.Fmt[24:28]),
		Priv:         binary.LittleEndian.Uint32(f.Fmt[28:32]),
		Flags:        binary.LittleEndian.Uint32(f.Fmt[32:36]),
		YCbCrEnc:     binary.LittleEndian.Uint32(f.Fmt[36:40]),
		Quantization: binary.LittleEndian.Uint32(f.Fmt[40:44]),
		XferFunc:     binary.LittleEndian.Uint32(f.Fmt[44:48]),
	}
}

func (f *v4l2Format) setPix(p v4l2PixFormat) {
	binary.LittleEndian.PutUint32(f.Fmt[0:4], p.Width)
	binary.LittleEndian.PutUint32(f.Fmt[4:8], p.Height)
	binary.LittleEndian.PutUint32(f.Fmt[8:12], p.PixelFormat)
	binary.LittleEndian.PutUint32(f.Fmt[12:16], p.Field)
	binary.LittleEndian.PutUint32(f.Fmt[16:20], p.BytesPerLine)
	binary.LittleEndian.PutUint32(f.Fmt[20:24], p.SizeImage)
	binary.LittleEndian.PutUint32(f.Fmt[24:28], p.Colorspace)
	binary.LittleEndian.PutUint32(f.Fmt[28:32], p.Priv)
	binary.LittleEndian.PutUint32(f.Fmt[32:36], p.Flags)
	binary.LittleEndian.PutUint32(f.Fmt[36:40], p.YCbCrEnc)
	binary.LittleEndian.PutUint32(f.Fmt[40:44], p.Quantization)
	binary.LittleEndian.PutUint32(f.Fmt[44:48], p.XferFunc)
}

type v4l2StreamParm struct {
	Type uint32
	Parm [200]byte
}

func (s *v4l2StreamParm) timePerFrame() (num, den uint32) {
	return binary.LittleEndian.Uint32(s.Parm[8:12]), binary.LittleEndian.Uint32(s.Parm[12:16])
}

func (s *v4l2StreamParm) setTimePerFrame(num, den uint32) {
	binary.LittleEndian.PutUint32(s.Parm[8:12], num)
	binary.LittleEndian.PutUint32(s.Parm[12:16], den)
}

type v4l2RequestBuffers struct {
	Count        uint32
	Type         uint32
	Memory       uint32
	Capabilities uint32
	Flags        uint8
	Reserved     [3]uint8
}

type v4l2Buffer struct {
	Index         uint32
	Type          uint32
	BytesUsed     uint32
	Flags         uint32
	Field         uint32
	_             uint32
	TimestampSec  int64
	TimestampUsec int64
	Timecode      [16]byte
	Sequence      uint32
	Memory        uint32
	M             uint64
	Length        uint32
	Reserved2     uint32
	RequestFD     int32
	_             int32
}

// DeviceInfo describes one V4L2 video capture node.
type DeviceInfo struct {
	Path    string
	Name    string
	Driver  string
	BusInfo string
}

// DisplayName formats the device for CLI/GUI selection lists.
func (d DeviceInfo) DisplayName() string {
	if d.Name == "" {
		return d.Path
	}
	return fmt.Sprintf("%s (%s)", d.Name, d.Path)
}

// Config describes one V4L2 capture.
type Config struct {
	Device  string  // "/dev/video0", "Name (/dev/video0)", or card name
	Width   int     // desired width (0 -> 1280)
	Height  int     // desired height (0 -> 720)
	FPS     float64 // target frame rate; closest supported rate is used
	Format  string  // "auto", "NV12", "YUYV", "YUY2", "YU12", ...
	Buffers int     // MMAP buffers to request (clamped to >= 4; default 4)
}

// Mode is a negotiated or advertised V4L2 capture mode.
type Mode struct {
	FourCC        string
	Width, Height int
	FPS           float64
}

// TimestampInfo records V4L2/uvcvideo timestamp details observed for the stream.
type TimestampInfo struct {
	Source       string // "SOE", "EOF", or ""
	HWTimestamps string // content of /sys/module/uvcvideo/parameters/hwtimestamps, or ""
}

// DevicePath extracts the /dev/videoN path when device is formatted as
// "Card Name (/dev/videoN)" or returns device unchanged.
func DevicePath(device string) string {
	if i := strings.LastIndex(device, "(/dev/"); i >= 0 {
		if j := strings.LastIndexByte(device, ')'); j > i+1 {
			return device[i+1 : j]
		}
	}
	return device
}

func fourccToString(v uint32) string {
	return string([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)})
}

func stringToFourCC(s string) (uint32, bool) {
	if len(s) != 4 {
		return 0, false
	}
	return uint32(s[0]) | uint32(s[1])<<8 | uint32(s[2])<<16 | uint32(s[3])<<24, true
}

func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return strings.TrimSpace(string(b[:i]))
		}
	}
	return strings.TrimSpace(string(b))
}

func effectiveCaps(c v4l2Capability) uint32 {
	if c.Capabilities&v4l2CapDeviceCaps != 0 {
		return c.DeviceCaps
	}
	return c.Capabilities
}

func isVideoCaptureCap(c v4l2Capability) bool {
	caps := effectiveCaps(c)
	if caps&v4l2CapMetaCapture != 0 {
		return false
	}
	return caps&v4l2CapVideoCapture != 0 && caps&v4l2CapStreaming != 0
}

type v4l2Device interface {
	Ioctl(req uintptr, ptr unsafe.Pointer) error
	Mmap(offset int64, length int) ([]byte, error)
	Munmap(data []byte) error
	Poll(timeoutMS int) (frameReady, woke bool, err error)
	Wake()
	Close() error
}

type v4l2System interface {
	Glob(pattern string) ([]string, error)
	Open(path string, nonblock bool) (v4l2Device, error)
	ReadFile(path string) ([]byte, error)
}

func listDeviceInfosWith(sys v4l2System) ([]DeviceInfo, error) {
	paths, err := sys.Glob("/dev/video*")
	if err != nil {
		return nil, err
	}
	sort.Slice(paths, func(i, j int) bool {
		return videoNodeLess(paths[i], paths[j])
	})
	var out []DeviceInfo
	for _, p := range paths {
		dev, err := sys.Open(p, false)
		if err != nil {
			continue
		}
		var cap v4l2Capability
		ierr := dev.Ioctl(vidiocQueryCap, unsafe.Pointer(&cap))
		_ = dev.Close()
		if ierr != nil || !isVideoCaptureCap(cap) {
			continue
		}
		out = append(out, DeviceInfo{
			Path:    p,
			Name:    cString(cap.Card[:]),
			Driver:  cString(cap.Driver[:]),
			BusInfo: cString(cap.BusInfo[:]),
		})
	}
	return out, nil
}

func videoNodeLess(a, b string) bool {
	ba, bb := filepath.Base(a), filepath.Base(b)
	if len(ba) != len(bb) {
		return len(ba) < len(bb)
	}
	return ba < bb
}

func resolveDevicePath(sys v4l2System, device string) (string, error) {
	p := DevicePath(strings.TrimSpace(device))
	if strings.HasPrefix(p, "/dev/") {
		return p, nil
	}
	infos, err := listDeviceInfosWith(sys)
	if err != nil {
		return "", err
	}
	var hits []DeviceInfo
	want := strings.ToLower(p)
	for _, d := range infos {
		if strings.EqualFold(d.DisplayName(), device) || strings.EqualFold(d.Name, device) || strings.EqualFold(d.Path, device) {
			return d.Path, nil
		}
		if want != "" && (strings.Contains(strings.ToLower(d.DisplayName()), want) || strings.Contains(strings.ToLower(d.Name), want)) {
			hits = append(hits, d)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0].Path, nil
	case 0:
		return "", fmt.Errorf("V4L2 device %q not found", device)
	default:
		return "", fmt.Errorf("V4L2 device %q is ambiguous (%d matches)", device, len(hits))
	}
}

func canonicalRequestedFormat(format string) string {
	switch strings.ToUpper(strings.TrimSpace(format)) {
	case "", "AUTO":
		return "AUTO"
	case "YUY2", "YUYV422", "YUYV":
		return "YUYV"
	case "I420", "IYUV", "YUV420P", "YU12":
		return "YU12"
	case "UYVY422", "UYVY":
		return "UYVY"
	case "P010LE", "P010":
		return "P010"
	case "RGB32", "BGR0", "BGRA", "BGRX", "XR24":
		return "XR24"
	case "BGR4":
		return "BGR4"
	case "RGB24", "BGR24", "BGR3":
		return "BGR3"
	case "RGB3":
		return "RGB3"
	default:
		return strings.ToUpper(strings.TrimSpace(format))
	}
}

func formatRank(want, fourcc string) int {
	if want != "AUTO" {
		if want == "XR24" && fourcc == "BGR4" {
			return 1
		}
		if fourcc == want {
			return 0
		}
		return -1
	}
	switch fourcc {
	case "NV12":
		return 0
	case "YU12", "YV12":
		return 1
	case "YUYV", "UYVY":
		return 2
	case "P010":
		return 3
	case "XR24", "BGR4":
		return 4
	case "BGR3", "RGB3":
		return 5
	}
	return -1
}

func listModesWith(dev v4l2Device) ([]Mode, error) {
	var modes []Mode
	for idx := uint32(0); ; idx++ {
		desc := v4l2FmtDesc{Index: idx, Type: v4l2BufTypeVideoCapture}
		if err := dev.Ioctl(vidiocEnumFmt, unsafe.Pointer(&desc)); err != nil {
			break
		}
		cc := fourccToString(desc.PixelFormat)
		if capture.DefaultPitch(cc, 1) == 0 {
			continue
		}
		sizes := enumFrameSizes(dev, desc.PixelFormat)
		if len(sizes) == 0 {
			continue
		}
		for _, sz := range sizes {
			ivals := enumFrameIntervals(dev, desc.PixelFormat, uint32(sz[0]), uint32(sz[1]))
			if len(ivals) == 0 {
				modes = append(modes, Mode{FourCC: cc, Width: sz[0], Height: sz[1]})
				continue
			}
			for _, fps := range ivals {
				modes = append(modes, Mode{FourCC: cc, Width: sz[0], Height: sz[1], FPS: fps})
			}
		}
	}
	return modes, nil
}

func enumFrameSizes(dev v4l2Device, pixfmt uint32) [][2]int {
	var sizes [][2]int
	for idx := uint32(0); ; idx++ {
		fs := v4l2FrmSizeEnum{Index: idx, PixelFormat: pixfmt}
		if err := dev.Ioctl(vidiocEnumFrameSizes, unsafe.Pointer(&fs)); err != nil {
			break
		}
		switch fs.Type {
		case v4l2FrmSizeTypeDiscrete:
			w := int(binary.LittleEndian.Uint32(fs.Data[0:4]))
			h := int(binary.LittleEndian.Uint32(fs.Data[4:8]))
			if w > 0 && h > 0 {
				sizes = append(sizes, [2]int{w, h})
			}
		case v4l2FrmSizeTypeContinuous, v4l2FrmSizeTypeStepwise:
			minW := int(binary.LittleEndian.Uint32(fs.Data[0:4]))
			maxW := int(binary.LittleEndian.Uint32(fs.Data[4:8]))
			minH := int(binary.LittleEndian.Uint32(fs.Data[12:16]))
			maxH := int(binary.LittleEndian.Uint32(fs.Data[16:20]))
			for _, cand := range [][2]int{{1280, 720}, {1920, 1080}, {2560, 1440}, {3840, 2160}} {
				if cand[0] >= minW && cand[0] <= maxW && cand[1] >= minH && cand[1] <= maxH {
					sizes = append(sizes, cand)
				}
			}
		}
	}
	return sizes
}

func enumFrameIntervals(dev v4l2Device, pixfmt, w, h uint32) []float64 {
	var fps []float64
	for idx := uint32(0); ; idx++ {
		fi := v4l2FrmIvalEnum{Index: idx, PixelFormat: pixfmt, Width: w, Height: h}
		if err := dev.Ioctl(vidiocEnumFrameIntervals, unsafe.Pointer(&fi)); err != nil {
			break
		}
		switch fi.Type {
		case v4l2FrmIvalTypeDiscrete:
			num := binary.LittleEndian.Uint32(fi.Data[0:4])
			den := binary.LittleEndian.Uint32(fi.Data[4:8])
			if num > 0 && den > 0 {
				fps = append(fps, float64(den)/float64(num))
			}
		case v4l2FrmIvalTypeContinuous, v4l2FrmIvalTypeStepwise:
			minNum := binary.LittleEndian.Uint32(fi.Data[0:4])
			minDen := binary.LittleEndian.Uint32(fi.Data[4:8])
			maxNum := binary.LittleEndian.Uint32(fi.Data[8:12])
			maxDen := binary.LittleEndian.Uint32(fi.Data[12:16])
			if minNum > 0 && minDen > 0 {
				fps = append(fps, float64(minDen)/float64(minNum))
			}
			if maxNum > 0 && maxDen > 0 {
				fps = append(fps, float64(maxDen)/float64(maxNum))
			}
		}
	}
	return fps
}

func negotiateMode(dev v4l2Device, cfg Config) (Mode, int, error) {
	want := canonicalRequestedFormat(cfg.Format)
	type candidate struct {
		fourcc string
		rank   int
		fps    float64
	}
	var cands []candidate
	var seen []string

	for idx := uint32(0); ; idx++ {
		desc := v4l2FmtDesc{Index: idx, Type: v4l2BufTypeVideoCapture}
		if err := dev.Ioctl(vidiocEnumFmt, unsafe.Pointer(&desc)); err != nil {
			break
		}
		cc := fourccToString(desc.PixelFormat)
		if capture.DefaultPitch(cc, 1) == 0 {
			continue
		}
		r := formatRank(want, cc)
		if r < 0 {
			continue
		}
		matchedSize := false
		hasEnumSizes := false
		for sidx := uint32(0); ; sidx++ {
			fs := v4l2FrmSizeEnum{Index: sidx, PixelFormat: desc.PixelFormat}
			if err := dev.Ioctl(vidiocEnumFrameSizes, unsafe.Pointer(&fs)); err != nil {
				break
			}
			hasEnumSizes = true
			switch fs.Type {
			case v4l2FrmSizeTypeDiscrete:
				w := int(binary.LittleEndian.Uint32(fs.Data[0:4]))
				h := int(binary.LittleEndian.Uint32(fs.Data[4:8]))
				if w == cfg.Width && h == cfg.Height {
					matchedSize = true
				}
			case v4l2FrmSizeTypeContinuous, v4l2FrmSizeTypeStepwise:
				minW := int(binary.LittleEndian.Uint32(fs.Data[0:4]))
				maxW := int(binary.LittleEndian.Uint32(fs.Data[4:8]))
				stepW := max(int(binary.LittleEndian.Uint32(fs.Data[8:12])), 1)
				minH := int(binary.LittleEndian.Uint32(fs.Data[12:16]))
				maxH := int(binary.LittleEndian.Uint32(fs.Data[16:20]))
				stepH := max(int(binary.LittleEndian.Uint32(fs.Data[20:24])), 1)
				if cfg.Width >= minW && cfg.Width <= maxW && cfg.Height >= minH && cfg.Height <= maxH &&
					(cfg.Width-minW)%stepW == 0 && (cfg.Height-minH)%stepH == 0 {
					matchedSize = true
				}
			}
			if matchedSize {
				break
			}
		}
		if hasEnumSizes && !matchedSize {
			continue
		}
		ivals := enumFrameIntervals(dev, desc.PixelFormat, uint32(cfg.Width), uint32(cfg.Height))
		if len(ivals) == 0 {
			seen = append(seen, cc)
			cands = append(cands, candidate{fourcc: cc, rank: r, fps: cfg.FPS})
			continue
		}
		for _, f := range ivals {
			seen = append(seen, fmt.Sprintf("%s@%.2f", cc, f))
			cands = append(cands, candidate{fourcc: cc, rank: r, fps: f})
		}
	}

	if len(cands) == 0 {
		if want != "AUTO" {
			if _, ok := stringToFourCC(want); ok {
				cands = append(cands, candidate{fourcc: want, rank: 0, fps: cfg.FPS})
			}
		}
	}
	if len(cands) == 0 {
		return Mode{}, 0, fmt.Errorf("no usable V4L2 %dx%d mode (format %s); offered %v", cfg.Width, cfg.Height, cfg.Format, seen)
	}

	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].rank != cands[j].rank {
			return cands[i].rank < cands[j].rank
		}
		if cfg.FPS > 0 {
			return math.Abs(cands[i].fps-cfg.FPS) < math.Abs(cands[j].fps-cfg.FPS)
		}
		return cands[i].fps > cands[j].fps
	})

	chosen := cands[0]
	fcc, _ := stringToFourCC(chosen.fourcc)
	var vfmt v4l2Format
	vfmt.Type = v4l2BufTypeVideoCapture
	_ = dev.Ioctl(vidiocGFmt, unsafe.Pointer(&vfmt))
	pix := vfmt.pix()
	pix.Width = uint32(cfg.Width)
	pix.Height = uint32(cfg.Height)
	pix.PixelFormat = fcc
	pix.Field = v4l2FieldAny
	pix.BytesPerLine = 0
	pix.SizeImage = 0
	vfmt.setPix(pix)
	if err := dev.Ioctl(vidiocSFmt, unsafe.Pointer(&vfmt)); err != nil {
		return Mode{}, 0, fmt.Errorf("VIDIOC_S_FMT %s %dx%d: %w", chosen.fourcc, cfg.Width, cfg.Height, err)
	}
	gotPix := vfmt.pix()
	gotCC := fourccToString(gotPix.PixelFormat)
	if capture.DefaultPitch(gotCC, 1) == 0 {
		return Mode{}, 0, fmt.Errorf("device negotiated unsupported format %q", gotCC)
	}

	fps := chosen.fps
	if fps <= 0 {
		fps = cfg.FPS
	}
	if fps > 0 {
		var parm v4l2StreamParm
		parm.Type = v4l2BufTypeVideoCapture
		if err := dev.Ioctl(vidiocGParm, unsafe.Pointer(&parm)); err == nil {
			num, den := fpsToInterval(fps)
			parm.setTimePerFrame(num, den)
			if err := dev.Ioctl(vidiocSParm, unsafe.Pointer(&parm)); err == nil {
				gn, gd := parm.timePerFrame()
				if gn > 0 && gd > 0 {
					fps = float64(gd) / float64(gn)
				}
			}
		}
	}

	return Mode{
		FourCC: gotCC,
		Width:  int(gotPix.Width),
		Height: int(gotPix.Height),
		FPS:    fps,
	}, int(gotPix.BytesPerLine), nil
}

func fpsToInterval(fps float64) (num, den uint32) {
	for _, std := range [][2]uint32{
		{1001, 24000}, {1001, 30000}, {1001, 48000},
		{1001, 60000}, {1001, 120000}, {1001, 240000},
	} {
		target := float64(std[1]) / float64(std[0])
		if math.Abs(fps-target)/target < 5e-4 {
			return std[0], std[1]
		}
	}
	rounded := math.Round(fps)
	if rounded >= 1 && math.Abs(fps-rounded)/rounded < 5e-4 {
		return 1, uint32(rounded)
	}
	return 1000, uint32(math.Round(fps * 1000))
}

func bufDeviceTicks(buf v4l2Buffer, arrive, freq int64) (ticks int64, src string) {
	if buf.Flags&v4l2BufFlagTimestampMask != v4l2BufFlagTimestampMonotonic {
		return 0, ""
	}
	if buf.Flags&v4l2BufFlagTstampSrcMask == v4l2BufFlagTstampSrcSOE {
		src = "SOE"
	} else {
		src = "EOF"
	}
	tsNS := buf.TimestampSec*1_000_000_000 + buf.TimestampUsec*1_000
	if tsNS <= 0 {
		return 0, src
	}
	ticks = nsToTicks(tsNS, freq)
	if ticks <= 0 || ticks > arrive {
		return 0, src
	}
	return ticks, src
}

func nsToTicks(ns, freq int64) int64 {
	if freq == 1_000_000_000 {
		return ns
	}
	return ns/1_000_000_000*freq + ns%1_000_000_000*freq/1_000_000_000
}

type bufState uint8

const (
	bufQueued bufState = iota
	bufInCallback
	bufHeld
	bufUnmapped
)

type mmapBuf struct {
	data     []byte
	state    bufState
	refCount int
}

// Source is a running V4L2 capture.
type Source struct {
	Mode       Mode
	Timestamps TimestampInfo

	stop    atomic.Bool
	dropped atomic.Uint64
	done    chan error

	mu           sync.Mutex
	dev          v4l2Device
	tsSource     string
	hwTimestamps string
	bufs         []mmapBuf
	queuedCount  int
	heldCount    int
	requeue      []uint32
	streamClosed bool
}

// Done delivers the reason the capture stopped (once).
func (s *Source) Done() <-chan error { return s.done }

// Stop ends the capture.
func (s *Source) Stop() {
	s.stop.Store(true)
	s.mu.Lock()
	dev := s.dev
	s.mu.Unlock()
	if dev != nil {
		dev.Wake()
	}
}

// Dropped returns the number of frames where Frame.Hold returned nil because
// all holdable MMAP buffers were already held.
func (s *Source) Dropped() uint64 { return s.dropped.Load() }

// TimestampInfo returns the V4L2 timestamp source ("SOE" or "EOF") and
// uvcvideo hwtimestamps parameter observed for the stream.
func (s *Source) TimestampInfo() TimestampInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return TimestampInfo{Source: s.tsSource, HWTimestamps: s.hwTimestamps}
}

var _ capture.Source = (*Source)(nil)

func startWith(sys v4l2System, cfg Config, now func() int64, freq int64, onFrame func(capture.Frame)) (*Source, error) {
	if cfg.Width <= 0 || cfg.Height <= 0 {
		cfg.Width, cfg.Height = 1280, 720
	}
	if cfg.Buffers < 4 {
		cfg.Buffers = 4
	}
	path, err := resolveDevicePath(sys, cfg.Device)
	if err != nil {
		return nil, err
	}
	hwTS := ""
	if b, err := sys.ReadFile(uvcHWTimestampsPath); err == nil {
		hwTS = strings.TrimSpace(string(b))
	}

	s := &Source{
		done:         make(chan error, 1),
		hwTimestamps: hwTS,
		Timestamps:   TimestampInfo{HWTimestamps: hwTS},
	}
	ready := make(chan error, 1)
	go func() {
		err := s.run(sys, path, cfg, now, freq, onFrame, ready)
		select {
		case ready <- err:
		default:
		}
		s.done <- err
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Source) run(sys v4l2System, path string, cfg Config, now func() int64, freq int64, onFrame func(capture.Frame), ready chan<- error) error {
	dev, err := sys.Open(path, true)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	s.mu.Lock()
	s.dev = dev
	s.mu.Unlock()

	cleanupEarly := true
	defer func() {
		if cleanupEarly {
			s.cleanupStream(dev, false)
		}
	}()

	var cap v4l2Capability
	if err := dev.Ioctl(vidiocQueryCap, unsafe.Pointer(&cap)); err != nil {
		return fmt.Errorf("VIDIOC_QUERYCAP %s: %w", path, err)
	}
	if !isVideoCaptureCap(cap) {
		return fmt.Errorf("%s is not a streaming V4L2 video capture device", path)
	}

	mode, pitch, err := negotiateMode(dev, cfg)
	if err != nil {
		return err
	}
	s.Mode = mode

	req := v4l2RequestBuffers{
		Count:  uint32(cfg.Buffers),
		Type:   v4l2BufTypeVideoCapture,
		Memory: v4l2MemoryMMAP,
	}
	if err := dev.Ioctl(vidiocReqBufs, unsafe.Pointer(&req)); err != nil {
		return fmt.Errorf("VIDIOC_REQBUFS(%d): %w", cfg.Buffers, err)
	}
	if req.Count < 2 {
		return fmt.Errorf("VIDIOC_REQBUFS granted only %d buffer(s), need >= 2", req.Count)
	}

	s.bufs = make([]mmapBuf, req.Count)
	for i := uint32(0); i < req.Count; i++ {
		vb := v4l2Buffer{
			Index:  i,
			Type:   v4l2BufTypeVideoCapture,
			Memory: v4l2MemoryMMAP,
		}
		if err := dev.Ioctl(vidiocQueryBuf, unsafe.Pointer(&vb)); err != nil {
			return fmt.Errorf("VIDIOC_QUERYBUF(%d): %w", i, err)
		}
		mem, err := dev.Mmap(int64(uint32(vb.M)), int(vb.Length))
		if err != nil {
			return fmt.Errorf("mmap buffer %d: %w", i, err)
		}
		s.bufs[i] = mmapBuf{data: mem, state: bufUnmapped}
		q := v4l2Buffer{Index: i, Type: v4l2BufTypeVideoCapture, Memory: v4l2MemoryMMAP}
		if err := dev.Ioctl(vidiocQBuf, unsafe.Pointer(&q)); err != nil {
			return fmt.Errorf("VIDIOC_QBUF(%d): %w", i, err)
		}
		s.bufs[i].state = bufQueued
		s.queuedCount++
	}

	bufType := uint32(v4l2BufTypeVideoCapture)
	if err := dev.Ioctl(vidiocStreamOn, unsafe.Pointer(&bufType)); err != nil {
		return fmt.Errorf("VIDIOC_STREAMON: %w", err)
	}
	cleanupEarly = false
	defer s.cleanupStream(dev, true)

	ready <- nil

	w, h := mode.Width, mode.Height
	var luma []byte
	if capture.NeedsLumaBuffer(mode.FourCC) {
		luma = make([]byte, w*h)
	}

	for !s.stop.Load() {
		if err := s.drainRequeue(dev); err != nil {
			return err
		}
		frameReady, _, err := dev.Poll(200)
		if err != nil {
			return fmt.Errorf("poll %s: %w", path, err)
		}
		if s.stop.Load() {
			break
		}
		if err := s.drainRequeue(dev); err != nil {
			return err
		}
		if !frameReady {
			continue
		}

		for !s.stop.Load() {
			if err := s.drainRequeue(dev); err != nil {
				return err
			}
			dq := v4l2Buffer{
				Type:   v4l2BufTypeVideoCapture,
				Memory: v4l2MemoryMMAP,
			}
			if err := dev.Ioctl(vidiocDQBuf, unsafe.Pointer(&dq)); err != nil {
				if isEAGAIN(err) {
					break
				}
				return fmt.Errorf("VIDIOC_DQBUF: %w", err)
			}
			arrive := now()
			idx := dq.Index
			if int(idx) >= len(s.bufs) {
				return fmt.Errorf("VIDIOC_DQBUF invalid index %d", idx)
			}
			if dq.Flags&v4l2BufFlagError != 0 {
				q := v4l2Buffer{Index: idx, Type: v4l2BufTypeVideoCapture, Memory: v4l2MemoryMMAP}
				if err := dev.Ioctl(vidiocQBuf, unsafe.Pointer(&q)); err != nil {
					return fmt.Errorf("VIDIOC_QBUF(%d): %w", idx, err)
				}
				continue
			}

			s.mu.Lock()
			s.queuedCount--
			s.bufs[idx].state = bufInCallback
			bufSlice := s.bufs[idx].data
			s.mu.Unlock()

			devTicks, tsSrc := bufDeviceTicks(dq, arrive, freq)
			if tsSrc != "" {
				s.mu.Lock()
				if s.tsSource == "" {
					s.tsSource = tsSrc
					s.Timestamps.Source = tsSrc
				}
				s.mu.Unlock()
			}

			used := len(bufSlice)
			if dq.BytesUsed > 0 && int(dq.BytesUsed) < used {
				used = int(dq.BytesUsed)
			}

			var callbackActive atomic.Bool
			callbackActive.Store(true)

			holdFn := func() func() {
				if !callbackActive.Load() {
					return nil
				}
				s.mu.Lock()
				defer s.mu.Unlock()
				if !callbackActive.Load() {
					return nil
				}
				b := &s.bufs[idx]
				switch b.state {
				case bufInCallback:
					// Never hold the final queued buffer or the driver would starve.
					if s.queuedCount == 0 {
						s.dropped.Add(1)
						return nil
					}
					b.state = bufHeld
					b.refCount = 1
					s.heldCount++
				case bufHeld:
					b.refCount++
				default:
					return nil
				}
				var once sync.Once
				return func() {
					once.Do(func() {
						s.releaseHeldBuffer(idx)
					})
				}
			}

			if y, stride, raw, rawPitch, ok := capture.ExtractLuma(mode.FourCC, bufSlice[:used], pitch, w, h, false, luma); ok {
				f := capture.WithHold(capture.Frame{
					Y:         y,
					Stride:    stride,
					Data:      raw,
					Pitch:     rawPitch,
					FourCC:    mode.FourCC,
					Width:     w,
					Height:    h,
					QPC:       arrive,
					DeviceQPC: devTicks,
				}, holdFn)
				onFrame(f)
			}

			callbackActive.Store(false)

			s.mu.Lock()
			stillInCallback := s.bufs[idx].state == bufInCallback
			if stillInCallback {
				s.bufs[idx].state = bufQueued
				s.queuedCount++
			}
			s.mu.Unlock()

			if stillInCallback {
				q := v4l2Buffer{Index: idx, Type: v4l2BufTypeVideoCapture, Memory: v4l2MemoryMMAP}
				if err := dev.Ioctl(vidiocQBuf, unsafe.Pointer(&q)); err != nil {
					return fmt.Errorf("VIDIOC_QBUF(%d): %w", idx, err)
				}
			}
		}
	}
	return nil
}

func (s *Source) drainRequeue(dev v4l2Device) error {
	s.mu.Lock()
	if len(s.requeue) == 0 {
		s.mu.Unlock()
		return nil
	}
	pending := make([]uint32, len(s.requeue))
	copy(pending, s.requeue)
	s.requeue = s.requeue[:0]
	for _, idx := range pending {
		s.bufs[idx].state = bufQueued
		s.queuedCount++
	}
	s.mu.Unlock()

	for _, idx := range pending {
		q := v4l2Buffer{Index: idx, Type: v4l2BufTypeVideoCapture, Memory: v4l2MemoryMMAP}
		if err := dev.Ioctl(vidiocQBuf, unsafe.Pointer(&q)); err != nil {
			return fmt.Errorf("VIDIOC_QBUF(%d): %w", idx, err)
		}
	}
	return nil
}

func (s *Source) releaseHeldBuffer(idx uint32) {
	s.mu.Lock()
	b := &s.bufs[idx]
	if b.state != bufHeld {
		s.mu.Unlock()
		return
	}
	b.refCount--
	if b.refCount > 0 {
		s.mu.Unlock()
		return
	}
	s.heldCount--
	if !s.streamClosed {
		s.requeue = append(s.requeue, idx)
		dev := s.dev
		s.mu.Unlock()
		if dev != nil {
			dev.Wake()
		}
		return
	}
	data := b.data
	b.data = nil
	b.state = bufUnmapped
	doneAll := s.heldCount == 0
	dev := s.dev
	if doneAll {
		s.dev = nil
	}
	s.mu.Unlock()

	if data != nil {
		_ = dev.Munmap(data)
	}
	if doneAll && dev != nil {
		req := v4l2RequestBuffers{Count: 0, Type: v4l2BufTypeVideoCapture, Memory: v4l2MemoryMMAP}
		_ = dev.Ioctl(vidiocReqBufs, unsafe.Pointer(&req))
		_ = dev.Close()
	}
}

func (s *Source) cleanupStream(dev v4l2Device, streaming bool) {
	if streaming {
		bufType := uint32(v4l2BufTypeVideoCapture)
		_ = dev.Ioctl(vidiocStreamOff, unsafe.Pointer(&bufType))
	}

	s.mu.Lock()
	s.streamClosed = true
	var toUnmap [][]byte
	for i := range s.bufs {
		if s.bufs[i].state != bufHeld && s.bufs[i].data != nil {
			toUnmap = append(toUnmap, s.bufs[i].data)
			s.bufs[i].data = nil
			s.bufs[i].state = bufUnmapped
		}
	}
	heldLeft := s.heldCount
	if heldLeft == 0 {
		s.dev = nil
	}
	s.mu.Unlock()

	for _, mem := range toUnmap {
		_ = dev.Munmap(mem)
	}
	if heldLeft == 0 {
		if len(s.bufs) > 0 {
			req := v4l2RequestBuffers{Count: 0, Type: v4l2BufTypeVideoCapture, Memory: v4l2MemoryMMAP}
			_ = dev.Ioctl(vidiocReqBufs, unsafe.Pointer(&req))
		}
		_ = dev.Close()
	}
}

func isEAGAIN(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "resource temporarily unavailable") || strings.Contains(err.Error(), "EAGAIN"))
}
