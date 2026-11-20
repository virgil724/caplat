//go:build windows

package caplat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/virgil724/caplat/internal/barcode"
	"github.com/virgil724/caplat/internal/capture"
	"github.com/virgil724/caplat/internal/capture/dshow"
	"github.com/virgil724/caplat/internal/capture/ffmpeg"
	"github.com/virgil724/caplat/internal/capture/mf"
	"github.com/virgil724/caplat/internal/displays"
	"github.com/virgil724/caplat/internal/measure"
	"github.com/virgil724/caplat/internal/present"
	"github.com/virgil724/caplat/internal/win"
)

// Display is one desktop output; Mode is one capture mode of a device.
type (
	Display = displays.Display
	Mode    = ffmpeg.Mode
)

// ErrAborted is returned when the user pressed Esc or clicked the
// measurement window. The result so far is still returned.
var ErrAborted = present.ErrAborted

// Displays lists the desktop's outputs.
func Displays() ([]Display, error) {
	win.SetDPIAware()
	return displays.List()
}

// Devices lists DirectShow video capture devices (through ffmpeg).
func Devices(ctx context.Context, ffmpegBin string) ([]string, error) {
	return ffmpeg.ListDevices(ctx, ffmpegBin)
}

// DevicesMF lists video capture devices through Media Foundation (no
// ffmpeg needed).
func DevicesMF() ([]string, error) { return mf.ListDevices() }

// PickDevice chooses the capture card among DirectShow devices ("" if
// that is ambiguous).
func PickDevice(devices []string) string { return displays.PickDevice(devices) }

// DevicesDShow lists DirectShow video capture devices in-process (no
// ffmpeg needed).
func DevicesDShow() ([]string, error) { return dshow.ListDevices() }

func apiName(backend string) string {
	if backend == BackendDShow {
		return "DirectShow"
	}
	return "Media Foundation"
}

// Modes lists a device's capture modes.
func Modes(ctx context.Context, ffmpegBin, device string) ([]Mode, error) {
	return ffmpeg.ListModes(ctx, ffmpegBin, device)
}

type resolved struct {
	setup   Setup
	display displays.Display
	ff      ffmpeg.Config
	mf      mf.Config // also used for dshow
	capW    int
	capH    int
}

func listDevices(ctx context.Context, cfg *Config) ([]string, error) {
	switch cfg.Backend {
	case BackendMF:
		return mf.ListDevices()
	case BackendDShow:
		return dshow.ListDevices()
	}
	return ffmpeg.ListDevices(ctx, cfg.FFmpeg)
}

func resolve(ctx context.Context, cfg *Config) (resolved, error) {
	cfg.defaults()
	if cfg.Backend != BackendFFmpeg && cfg.Backend != BackendMF && cfg.Backend != BackendDShow {
		return resolved{}, fmt.Errorf("unknown backend %q (want %s, %s or %s)", cfg.Backend, BackendMF, BackendDShow, BackendFFmpeg)
	}
	list, err := Displays()
	if err != nil {
		return resolved{}, err
	}
	d, err := displays.Find(list, cfg.Display)
	if err != nil {
		return resolved{}, err
	}
	if d.Primary && !cfg.Force {
		return resolved{}, ErrPrimary
	}
	dev := cfg.Device
	if dev == "" {
		devs, err := listDevices(ctx, cfg)
		if err != nil {
			return resolved{}, err
		}
		if dev = displays.PickDevice(devs); dev == "" {
			return resolved{}, fmt.Errorf("choose a capture device with --device; found %q", devs)
		}
	}
	r := resolved{display: d, capW: cfg.CaptureWidth, capH: cfg.CaptureHeight}
	r.setup = Setup{Display: d.Name, DisplayName: d.FriendlyName, OutputMode: d.Mode(), Device: dev, Backend: cfg.Backend}

	if cfg.Backend == BackendMF || cfg.Backend == BackendDShow {
		r.mf = mf.Config{Device: dev, Width: cfg.CaptureWidth, Height: cfg.CaptureHeight, FPS: d.RefreshHz(), Format: cfg.PixelFormat}
		r.setup.CaptureMode = fmt.Sprintf("%dx%d@%s %s (%s)", cfg.CaptureWidth, cfg.CaptureHeight, d.FrameRate(), cfg.PixelFormat, apiName(cfg.Backend))
		return r, nil
	}
	pf := cfg.PixelFormat
	if strings.EqualFold(pf, "auto") {
		modes, err := ffmpeg.ListModes(ctx, cfg.FFmpeg, dev)
		if err != nil {
			return resolved{}, err
		}
		if pf, err = ffmpeg.PickFormat(modes, cfg.CaptureWidth, cfg.CaptureHeight, d.RefreshHz()); err != nil {
			return resolved{}, fmt.Errorf("%s: %w", dev, err)
		}
	}
	r.ff = ffmpeg.Config{FFmpeg: cfg.FFmpeg, Device: dev, Width: cfg.CaptureWidth, Height: cfg.CaptureHeight,
		FrameRate: d.FrameRate(), PixelFormat: pf, Preset: cfg.Preset, ExtraIn: cfg.FFmpegIn, ExtraOut: cfg.FFmpegOut}
	args, err := ffmpeg.Args(r.ff)
	if err != nil {
		return resolved{}, err
	}
	r.setup.CaptureMode = fmt.Sprintf("%dx%d@%s %s", r.ff.Width, r.ff.Height, r.ff.FrameRate, pf)
	r.setup.Preset, r.setup.FFmpegArgs = cfg.Preset, args
	return r, nil
}

// Prepare resolves the display, device and capture mode without measuring.
func Prepare(ctx context.Context, cfg Config) (Setup, error) {
	r, err := resolve(ctx, &cfg)
	return r.setup, err
}

// Run measures until cfg.Duration passes, ctx ends or the user aborts.
// updates (optional) receives an Update about once a second; sends never
// block, so give it a buffer. The result is returned even on ErrAborted.
func Run(ctx context.Context, cfg Config, updates chan<- Update) (Result, error) {
	r, err := resolve(ctx, &cfg)
	if err != nil {
		return Result{}, err
	}
	res := Result{Setup: r.setup}
	freq := win.QPCFrequency()
	geo := barcode.Geometry{OutW: r.display.Width, OutH: r.display.Height, CapW: r.capW, CapH: r.capH}

	var (
		mu                 sync.Mutex
		total, sec, window measure.Histogram
		devTotal, devWin   measure.Histogram
		csv                *bufio.Writer
	)
	if cfg.CSV != nil {
		csv = bufio.NewWriter(cfg.CSV)
		fmt.Fprintf(csv, "# caplat display=%s (%s) output=%s device=%q backend=%s capture=%s preset=%s\n",
			r.setup.Display, r.setup.DisplayName, r.setup.OutputMode, r.setup.Device, r.setup.Backend, r.setup.CaptureMode, r.setup.Preset)
		fmt.Fprintln(csv, "frame,vblank_qpc,arrive_qpc,latency_ms,device_qpc,device_ms")
		defer csv.Flush()
	}
	m := measure.NewMatcher(freq, func(s measure.Sample) {
		mu.Lock()
		total.Add(s.LatencyMS)
		sec.Add(s.LatencyMS)
		window.Add(s.LatencyMS)
		if !math.IsNaN(s.DeviceMS) {
			devTotal.Add(s.DeviceMS)
			devWin.Add(s.DeviceMS)
		}
		if csv != nil {
			dev := ""
			if !math.IsNaN(s.DeviceMS) {
				dev = fmt.Sprintf("%.3f", s.DeviceMS)
			}
			fmt.Fprintf(csv, "%d,%d,%d,%.3f,%d,%s\n", s.Frame, s.VblankQPC, s.ArriveQPC, s.LatencyMS, s.DeviceQPC, dev)
		}
		mu.Unlock()
	})
	m.Settle = 10

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	onFrame := func(f capture.Frame) {
		code, ok := geo.Decode(f.Y, f.Stride)
		m.Captured(code, ok, f.QPC, f.DeviceQPC)
	}
	var src capture.Source
	switch cfg.Backend {
	case BackendDShow:
		c := r.mf
		s, err := dshow.Start(dshow.Config{Device: c.Device, Width: c.Width, Height: c.Height, FPS: c.FPS, Format: c.Format}, win.QPC, onFrame)
		if err != nil {
			return res, err
		}
		res.Setup.CaptureMode = fmt.Sprintf("%dx%d@%.3f %s (DirectShow)", s.Mode.Width, s.Mode.Height, s.Mode.FPS, s.Mode.FourCC)
		src = s
	case BackendMF:
		s, err := mf.Start(r.mf, win.QPC, freq, onFrame)
		if err != nil {
			return res, err
		}
		res.Setup.CaptureMode = fmt.Sprintf("%dx%d@%.3f %s (Media Foundation)", s.Mode.Width, s.Mode.Height, s.Mode.FPS, s.Mode.FourCC)
		src = s
	default:
		s, err := ffmpeg.Start(runCtx, r.ff, win.QPC, onFrame)
		if err != nil {
			return res, err
		}
		src = s
	}
	defer src.Stop()

	hz := r.display.RefreshHz()
	if hz <= 0 {
		hz = 60
	}
	pcfg := present.Config{
		Display:    r.display,
		WarmFrames: int(cfg.Warmup.Seconds() * hz),
		OnVblank:   m.Vblank,
	}
	if cfg.Duration > 0 {
		pcfg.Frames = uint64(cfg.Duration.Seconds() * hz)
	}
	started := make(chan time.Time, 1)
	pcfg.OnStart = func() { started <- time.Now() }

	presErr := make(chan error, 1)
	go func() { presErr <- present.Run(runCtx, pcfg) }()

	// Wait for measuring to start (or an early failure).
	var t0 time.Time
	select {
	case t0 = <-started:
	case err := <-presErr:
		return res, fmt.Errorf("presenter: %w", err)
	case err := <-src.Done():
		cancel()
		<-presErr
		return res, fmt.Errorf("capture: %w", err)
	}

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	nextWindow := cfg.Interval
	var runErr error
loop:
	for {
		select {
		case err := <-presErr:
			runErr = err
			break loop
		case err := <-src.Done():
			cancel()
			<-presErr
			runErr = fmt.Errorf("capture stopped early: %w", err)
			break loop
		case <-tick.C:
			el := time.Since(t0)
			mu.Lock()
			hc := total
			u := Update{Elapsed: el, Second: sec.Stats(), Total: total.Stats(), Hist: &hc, Device: devTotal.Stats()}
			sec.Reset()
			if el >= nextWindow {
				iv := Interval{Start: nextWindow - cfg.Interval, End: el, Stats: window.Stats(), Device: devWin.Stats()}
				res.Intervals = append(res.Intervals, iv)
				u.Interval = &iv
				window.Reset()
				devWin.Reset()
				nextWindow += cfg.Interval
			}
			mu.Unlock()
			u.Counts = m.Counts()
			if updates != nil {
				select {
				case updates <- u:
				default:
				}
			}
		}
	}

	// Let the last frames arrive, then stop the capture.
	m.PresenterStopped()
	if !errors.Is(runErr, present.ErrAborted) {
		time.Sleep(1500 * time.Millisecond)
	}
	cancel()
	src.Stop()
	m.Flush()
	mu.Lock()
	res.Elapsed = time.Since(t0)
	if window.N > 0 {
		res.Intervals = append(res.Intervals, Interval{Start: nextWindow - cfg.Interval, End: res.Elapsed, Stats: window.Stats(), Device: devWin.Stats()})
	}
	res.Total, res.Hist, res.Device = total.Stats(), total, devTotal.Stats()
	mu.Unlock()
	res.Counts = m.Counts()
	return res, runErr
}
