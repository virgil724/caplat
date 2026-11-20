//go:build windows

// Package mf reads a capture device in-process with Media Foundation's
// IMFSourceReader (synchronous ReadSample on its own locked thread), with no
// DirectShow, ffmpeg or pipe in between. Every frame carries the driver's
// capture timestamp (MFSampleExtension_DeviceTimestamp), which is on the
// QueryPerformanceCounter clock: the delay splits into the card and USB
// before it, and the software after it.
package mf

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/virgil724/caplat/internal/capture"
	"github.com/virgil724/caplat/internal/win"
)

// Config describes one capture.
type Config struct {
	Device string
	Width  int
	Height int
	FPS    float64 // target frame rate; the closest native rate is used
	Format string  // "auto" (NV12, then other planar, then YUY2), or a FourCC
}

// Mode is a native mode of a device.
type Mode struct {
	FourCC        string
	Width, Height int
	FPS           float64
}

// luma formats this backend can read: planar formats whose Y plane comes
// first, and YUY2 (Y in every other byte).
var lumaFormats = map[string]bool{"NV12": true, "I420": true, "YV12": true, "IYUV": true, "YUY2": true}

// withMF runs f on a locked thread with COM and Media Foundation started.
func withMF(f func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := win.COInitMultithreaded(); err != nil {
		return fmt.Errorf("COM: %w", err)
	}
	defer win.COUninit()
	if err := win.MFStartup(); err != nil {
		return fmt.Errorf("Media Foundation: %w", err)
	}
	defer win.MFShutdown()
	return f()
}

// ListDevices returns the video capture devices' names.
func ListDevices() ([]string, error) {
	var names []string
	err := withMF(func() error {
		devs, err := win.EnumVideoCaptureDevices()
		if err != nil {
			return err
		}
		for _, d := range devs {
			if n, err := d.GetString(&win.MF_DEVSOURCE_ATTRIBUTE_FRIENDLY_NAME); err == nil {
				names = append(names, n)
			}
			d.Release()
		}
		return nil
	})
	return names, err
}

// Source is a running Media Foundation capture.
type Source struct {
	stop atomic.Bool
	done chan error
	Mode Mode // the mode chosen
}

// Done delivers the reason the capture stopped.
func (s *Source) Done() <-chan error { return s.done }

// Stop ends the capture after the frame being read.
func (s *Source) Stop() { s.stop.Store(true) }

var _ capture.Source = (*Source)(nil)

// Start opens the device and calls onFrame for every frame, on the capture
// thread. now is the QPC clock; freq its ticks per second.
func Start(cfg Config, now func() int64, freq int64, onFrame func(capture.Frame)) (*Source, error) {
	s := &Source{done: make(chan error, 1)}
	ready := make(chan error, 1)
	go func() {
		err := withMF(func() error { return s.run(cfg, now, freq, onFrame, ready) })
		select {
		case ready <- err: // failed before it was ready
		default:
		}
		s.done <- err
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Source) run(cfg Config, now func() int64, freq int64, onFrame func(capture.Frame), ready chan<- error) error {
	devs, err := win.EnumVideoCaptureDevices()
	if err != nil {
		return err
	}
	var dev win.Attributes
	for _, d := range devs {
		n, _ := d.GetString(&win.MF_DEVSOURCE_ATTRIBUTE_FRIENDLY_NAME)
		if dev.Object == 0 && n == cfg.Device {
			dev = d
			continue
		}
		d.Release()
	}
	if dev.Object == 0 {
		return fmt.Errorf("Media Foundation has no video device %q", cfg.Device)
	}
	defer dev.Release()
	src, err := dev.ActivateMediaSource()
	if err != nil {
		return fmt.Errorf("open %q: %w", cfg.Device, err)
	}
	defer func() { win.ShutdownMediaSource(src); src.Release() }()

	attrs, err := win.NewAttributes(2)
	if err != nil {
		return err
	}
	defer attrs.Release()
	attrs.SetUINT32(&win.MF_LOW_LATENCY, 1)
	attrs.SetUINT32(&win.MF_READWRITE_DISABLE_CONVERTERS, 1) // native frames only
	rd, err := win.NewSourceReader(src, attrs)
	if err != nil {
		return fmt.Errorf("source reader: %w", err)
	}
	defer rd.Release()

	const stream = win.MF_SOURCE_READER_FIRST_VIDEO_STREAM
	t, mode, err := pickType(rd, cfg)
	if err != nil {
		return err
	}
	s.Mode = mode
	rd.SetStreamSelection(win.MF_SOURCE_READER_ALL_STREAMS, false)
	rd.SetStreamSelection(stream, true)
	err = rd.SetCurrentMediaType(stream, t)
	t.Release()
	if err != nil {
		return fmt.Errorf("set %s %dx%d@%.3f: %w", mode.FourCC, mode.Width, mode.Height, mode.FPS, err)
	}
	ready <- nil

	w, h := mode.Width, mode.Height
	yuy2 := mode.FourCC == "YUY2"
	var luma []byte
	if yuy2 {
		luma = make([]byte, w*h)
	}
	for !s.stop.Load() {
		sample, flags, ts, err := rd.ReadSample(stream)
		arrive := now()
		if err != nil {
			return fmt.Errorf("read sample: %w", err)
		}
		if flags&(win.MF_SOURCE_READERF_ERROR|win.MF_SOURCE_READERF_ENDOFSTREAM) != 0 {
			if sample.Object != 0 {
				sample.Release()
			}
			return errors.New("the capture stream ended")
		}
		if sample.Object == 0 {
			continue // stream tick
		}
		devTS := ts
		if v, err := sample.GetUINT64(&win.MFSampleExtension_DeviceTimestamp); err == nil {
			devTS = int64(v)
		}
		f := capture.Frame{QPC: arrive, DeviceQPC: hnsToQPC(devTS, freq)}
		if buf, err := win.SampleBuffer(sample); err == nil {
			if data, pitch, unlock, err := win.LockBuffer(buf); err == nil {
				if yuy2 {
					stride := pitch
					if stride == 0 {
						stride = w * 2
					}
					for y := 0; y < h; y++ {
						row := data[y*stride : y*stride+w*2]
						for x := 0; x < w; x++ {
							luma[y*w+x] = row[2*x]
						}
					}
					f.Y, f.Stride = luma, w
				} else {
					if pitch == 0 {
						pitch = w
					}
					f.Y, f.Stride = data[:pitch*h], pitch
				}
				onFrame(f)
				unlock()
			}
			buf.Release()
		}
		sample.Release()
	}
	return nil
}

// hnsToQPC converts 100 ns units of QPC time to QPC ticks without overflow.
func hnsToQPC(hns, freq int64) int64 {
	if freq == 10_000_000 {
		return hns
	}
	return hns/10_000_000*freq + hns%10_000_000*freq/10_000_000
}

// pickType chooses a native media type of the requested size: the wanted
// FourCC (or, for "auto", NV12 first, then other planar formats, then
// YUY2), at the frame rate closest to cfg.FPS.
func pickType(rd win.SourceReader, cfg Config) (win.Attributes, Mode, error) {
	want := strings.ToUpper(cfg.Format)
	rank := func(cc string) int {
		if want != "" && want != "AUTO" {
			if cc == want {
				return 0
			}
			return -1
		}
		switch cc {
		case "NV12":
			return 0
		case "I420", "IYUV", "YV12":
			return 1
		case "YUY2":
			return 2
		}
		return -1
	}
	var best win.Attributes
	var bestMode Mode
	bestRank, bestDiff := 99, math.Inf(1)
	var seen []string
	for i := uint32(0); ; i++ {
		t, err := rd.NativeMediaType(win.MF_SOURCE_READER_FIRST_VIDEO_STREAM, i)
		if err != nil {
			break
		}
		sub, _ := t.GetGUID(&win.MF_MT_SUBTYPE)
		fs, _ := t.GetUINT64(&win.MF_MT_FRAME_SIZE)
		fr, _ := t.GetUINT64(&win.MF_MT_FRAME_RATE)
		m := Mode{FourCC: win.FourCC(sub), Width: int(fs >> 32), Height: int(fs & 0xffffffff)}
		if d := fr & 0xffffffff; d != 0 {
			m.FPS = float64(fr>>32) / float64(d)
		}
		if m.Width == cfg.Width && m.Height == cfg.Height {
			seen = append(seen, fmt.Sprintf("%s@%.2f", m.FourCC, m.FPS))
		}
		r := rank(m.FourCC)
		diff := math.Abs(m.FPS - cfg.FPS)
		if m.Width != cfg.Width || m.Height != cfg.Height || r < 0 || !lumaFormats[m.FourCC] ||
			r > bestRank || (r == bestRank && diff >= bestDiff) {
			t.Release()
			continue
		}
		if best.Object != 0 {
			best.Release()
		}
		best, bestMode, bestRank, bestDiff = t, m, r, diff
	}
	if best.Object == 0 {
		return best, Mode{}, fmt.Errorf("no usable %dx%d mode (format %s); the device offers %v", cfg.Width, cfg.Height, cfg.Format, seen)
	}
	return best, bestMode, nil
}
