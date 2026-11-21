//go:build windows

// Package dshow reads a capture device in-process with DirectShow, without
// ffmpeg: capture filter -> SampleGrabber -> NullRenderer, with every sample
// delivered to a callback on DirectShow's streaming thread. The graph runs
// without a reference clock, so nothing waits for a sample's presentation
// time. DirectShow gives no driver timestamp on the QPC clock, so only the
// total latency is measured.
package dshow

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"

	"github.com/virgil724/caplat/capture"
	"github.com/virgil724/caplat/internal/win"
)

// Config describes one capture.
type Config struct {
	Device string
	Width  int
	Height int
	FPS    float64 // target frame rate
	Format string  // "auto" (NV12, then other planar, then YUY2), or a FourCC
}

// Mode is the mode the capture runs in.
type Mode struct {
	FourCC        string
	Width, Height int
	FPS           float64
}

var lumaFormats = map[string]int{
	"NV12": 0, "I420": 1, "IYUV": 1, "YV12": 1,
	"YUY2": 2, "UYVY": 2,
	"P010": 3, "RGB32": 4, "RGB24": 5,
}

func withCOM(f func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := win.COInitMultithreaded(); err != nil {
		return fmt.Errorf("COM: %w", err)
	}
	defer win.COUninit()
	return f()
}

// ListDevices returns the DirectShow video capture devices' names.
func ListDevices() ([]string, error) {
	var names []string
	err := withCOM(func() error {
		devs, err := win.EnumDShowVideoDevices()
		for _, d := range devs {
			names = append(names, d.Name)
			d.Moniker.Release()
		}
		return err
	})
	return names, err
}

// Source is a running DirectShow capture.
type Source struct {
	Mode     Mode
	done     chan error
	stop     chan struct{}
	stopOnce sync.Once
	cb       *win.GrabberCallback // kept reachable while the graph runs
}

var _ capture.Source = (*Source)(nil)

// Done delivers the reason the capture stopped.
func (s *Source) Done() <-chan error { return s.done }

// Stop stops the graph.
func (s *Source) Stop() { s.stopOnce.Do(func() { close(s.stop) }) }

// Start opens the device and calls onFrame for every frame, on DirectShow's
// streaming thread. now is the QPC clock.
func Start(cfg Config, now func() int64, onFrame func(capture.Frame)) (*Source, error) {
	s := &Source{done: make(chan error, 1), stop: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		err := withCOM(func() error { return s.run(cfg, now, onFrame, ready) })
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

func (s *Source) run(cfg Config, now func() int64, onFrame func(capture.Frame), ready chan<- error) error {
	devs, err := win.EnumDShowVideoDevices()
	if err != nil {
		return err
	}
	var moniker win.Object
	for _, d := range devs {
		if moniker == 0 && d.Name == cfg.Device {
			moniker = d.Moniker
			continue
		}
		d.Moniker.Release()
	}
	if moniker == 0 {
		return fmt.Errorf("DirectShow has no video device %q", cfg.Device)
	}
	defer moniker.Release()

	graph, err := win.CoCreate(&win.CLSID_FilterGraph, &win.IID_IGraphBuilder)
	if err != nil {
		return fmt.Errorf("filter graph: %w", err)
	}
	defer graph.Release()
	cgb, err := win.CoCreate(&win.CLSID_CaptureGraphBuilder2, &win.IID_ICaptureGraphBuilder2)
	if err != nil {
		return fmt.Errorf("capture graph builder: %w", err)
	}
	defer cgb.Release()
	if err := win.SetFiltergraph(cgb, graph); err != nil {
		return err
	}

	src, err := win.BindFilter(moniker)
	if err != nil {
		return fmt.Errorf("open %q: %w", cfg.Device, err)
	}
	defer src.Release()
	if err := win.AddFilter(graph, src, "capture"); err != nil {
		return err
	}

	mode, bottomUp, err := setFormat(cgb, src, cfg)
	if err != nil {
		return err
	}
	s.Mode = mode

	grabF, err := win.CoCreate(&win.CLSID_SampleGrabber, &win.IID_IBaseFilter)
	if err != nil {
		return fmt.Errorf("SampleGrabber (qedit.dll): %w", err)
	}
	defer grabF.Release()
	if err := win.AddFilter(graph, grabF, "grabber"); err != nil {
		return err
	}
	sg, err := grabF.QueryInterface(&win.IID_ISampleGrabber)
	if err != nil {
		return err
	}
	defer sg.Release()

	w, h := mode.Width, mode.Height
	planar420 := mode.FourCC == "NV12" || mode.FourCC == "I420" || mode.FourCC == "IYUV" || mode.FourCC == "YV12" || mode.FourCC == "P010"
	var luma []byte
	if capture.NeedsLumaBuffer(mode.FourCC) {
		luma = make([]byte, w*h)
	}
	s.cb = win.NewGrabberCallback(func(sample win.Object) {
		arrive := now()
		data := win.SampleData(sample)
		var stride int
		if planar420 {
			stride = len(data) * 2 / (h * 3) // 4:2:0: Y plane, then h/2 rows of chroma
		} else {
			stride = len(data) / h // packed rows may be padded (e.g. 4-byte DIB alignment)
		}
		y, yStride, raw, rawPitch, ok := capture.ExtractLuma(mode.FourCC, data, stride, w, h, bottomUp, luma)
		if !ok {
			return
		}
		onFrame(capture.Frame{
			Y: y, Stride: yStride,
			Data: raw, Pitch: rawPitch,
			FourCC: mode.FourCC, Width: w, Height: h,
			QPC: arrive,
		})
	})
	if err := win.SampleGrabber(sg, win.DShowSubtypeGUID(mode.FourCC), s.cb); err != nil {
		return fmt.Errorf("SampleGrabber: %w", err)
	}

	null, err := win.CoCreate(&win.CLSID_NullRenderer, &win.IID_IBaseFilter)
	if err != nil {
		return fmt.Errorf("NullRenderer: %w", err)
	}
	defer null.Release()
	if err := win.AddFilter(graph, null, "null"); err != nil {
		return err
	}
	if err := win.RenderStream(cgb, src, grabF, null); err != nil {
		return fmt.Errorf("connect %s %dx%d: %w", mode.FourCC, w, h, err)
	}
	if mt, err := win.ConnectedType(sg); err == nil {
		cw, ch, _, verr := mt.Video()
		mt.FreeFormat()
		if verr == nil && (cw != w || ch != h) {
			return fmt.Errorf("connected at %dx%d, not %dx%d", cw, ch, w, h)
		}
	}
	if err := win.RemoveClock(graph); err != nil {
		return fmt.Errorf("remove the graph clock: %w", err)
	}

	control, err := graph.QueryInterface(&win.IID_IMediaControl)
	if err != nil {
		return err
	}
	defer control.Release()
	if err := win.RunGraph(control); err != nil {
		return fmt.Errorf("run: %w", err)
	}
	ready <- nil
	<-s.stop
	win.StopGraph(control) // returns once the streaming thread is done
	runtime.KeepAlive(s.cb)
	return nil
}

// setFormat picks the capture pin's capability of the requested size (NV12
// first for "auto"), sets the frame interval closest to cfg.FPS within its
// limits, and applies it.
func setFormat(cgb, src win.Object, cfg Config) (Mode, bool, error) {
	sc, err := win.StreamConfig(cgb, src)
	if err != nil {
		return Mode{}, false, fmt.Errorf("capture pin format: %w", err)
	}
	defer sc.Release()
	n, err := win.StreamCaps(sc)
	if err != nil {
		return Mode{}, false, err
	}
	want := strings.ToUpper(cfg.Format)
	interval := int64(math.Round(1e7 / cfg.FPS))
	bestIdx, bestRank, bestDiff := -1, 99, math.Inf(1)
	var bestInterval int64
	var seen []string
	for i := 0; i < n; i++ {
		mt, caps, err := win.StreamCap(sc, i)
		if err != nil {
			continue
		}
		cc := win.DShowSubtypeName(mt.SubType)
		w, h, _, verr := mt.Video()
		win.DeleteMediaType(mt)
		if verr != nil || w != cfg.Width || h != cfg.Height {
			continue
		}
		seen = append(seen, cc)
		rank, ok := lumaFormats[cc]
		if !ok || (want != "" && want != "AUTO" && cc != want) {
			continue
		}
		iv := interval
		if caps.MinFrameInterval > 0 && iv < caps.MinFrameInterval {
			iv = caps.MinFrameInterval
		}
		if caps.MaxFrameInterval > 0 && iv > caps.MaxFrameInterval {
			iv = caps.MaxFrameInterval
		}
		diff := math.Abs(1e7/float64(iv) - cfg.FPS)
		if rank < bestRank || (rank == bestRank && diff < bestDiff) {
			bestIdx, bestRank, bestDiff, bestInterval = i, rank, diff, iv
		}
	}
	if bestIdx < 0 {
		return Mode{}, false, fmt.Errorf("no usable %dx%d format (format %s); the device offers %v", cfg.Width, cfg.Height, cfg.Format, seen)
	}
	mt, _, err := win.StreamCap(sc, bestIdx)
	if err != nil {
		return Mode{}, false, err
	}
	defer win.DeleteMediaType(mt)
	mt.SetInterval(bestInterval)
	mode := Mode{FourCC: win.DShowSubtypeName(mt.SubType), Width: cfg.Width, Height: cfg.Height, FPS: 1e7 / float64(bestInterval)}
	bottomUp := mt.BottomUp()
	if err := win.SetStreamFormat(sc, mt); err != nil {
		return Mode{}, false, fmt.Errorf("set %s %dx%d@%.3f: %w", mode.FourCC, mode.Width, mode.Height, mode.FPS, err)
	}
	return mode, bottomUp, nil
}
