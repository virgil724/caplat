//go:build windows

package caplat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/virgil724/caplat/internal/barcode"
	"github.com/virgil724/caplat/internal/capture/ffmpeg"
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

// Devices lists DirectShow video capture devices.
func Devices(ctx context.Context, ffmpegBin string) ([]string, error) {
	return ffmpeg.ListDevices(ctx, ffmpegBin)
}

// PickDevice chooses the capture card among DirectShow devices ("" if
// that is ambiguous).
func PickDevice(devices []string) string { return displays.PickDevice(devices) }

// Modes lists a device's capture modes.
func Modes(ctx context.Context, ffmpegBin, device string) ([]Mode, error) {
	return ffmpeg.ListModes(ctx, ffmpegBin, device)
}

type resolved struct {
	setup   Setup
	display displays.Display
	ff      ffmpeg.Config
}

func resolve(ctx context.Context, cfg *Config) (resolved, error) {
	cfg.defaults()
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
		devs, err := ffmpeg.ListDevices(ctx, cfg.FFmpeg)
		if err != nil {
			return resolved{}, err
		}
		if dev = displays.PickDevice(devs); dev == "" {
			return resolved{}, fmt.Errorf("choose a capture device with --device; found %q", devs)
		}
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
	ff := ffmpeg.Config{FFmpeg: cfg.FFmpeg, Device: dev, Width: cfg.CaptureWidth, Height: cfg.CaptureHeight,
		FrameRate: d.FrameRate(), PixelFormat: pf, Preset: cfg.Preset, ExtraIn: cfg.FFmpegIn, ExtraOut: cfg.FFmpegOut}
	args, err := ffmpeg.Args(ff)
	if err != nil {
		return resolved{}, err
	}
	return resolved{
		display: d,
		ff:      ff,
		setup: Setup{
			Display: d.Name, DisplayName: d.FriendlyName, OutputMode: d.Mode(),
			Device: dev, CaptureMode: fmt.Sprintf("%dx%d@%s %s", ff.Width, ff.Height, ff.FrameRate, pf),
			Preset: cfg.Preset, FFmpegArgs: args,
		},
	}, nil
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
	geo := barcode.Geometry{OutW: r.display.Width, OutH: r.display.Height, CapW: r.ff.Width, CapH: r.ff.Height}

	var (
		mu                 sync.Mutex
		total, sec, window measure.Histogram
		csv                *bufio.Writer
	)
	if cfg.CSV != nil {
		csv = bufio.NewWriter(cfg.CSV)
		fmt.Fprintf(csv, "# caplat display=%s (%s) output=%s device=%q capture=%s preset=%s\n",
			r.setup.Display, r.setup.DisplayName, r.setup.OutputMode, r.setup.Device, r.setup.CaptureMode, r.setup.Preset)
		fmt.Fprintln(csv, "frame,vblank_qpc,arrive_qpc,latency_ms")
		defer csv.Flush()
	}
	m := measure.NewMatcher(freq, func(s measure.Sample) {
		mu.Lock()
		total.Add(s.LatencyMS)
		sec.Add(s.LatencyMS)
		window.Add(s.LatencyMS)
		if csv != nil {
			fmt.Fprintf(csv, "%d,%d,%d,%.3f\n", s.Frame, s.VblankQPC, s.ArriveQPC, s.LatencyMS)
		}
		mu.Unlock()
	})

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	src, err := ffmpeg.Start(runCtx, r.ff, win.QPC, func(f ffmpeg.Frame) {
		code, ok := geo.Decode(f.Y, r.ff.Width)
		m.Captured(code, ok, f.QPC)
	})
	if err != nil {
		return res, err
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
			u := Update{Elapsed: el, Second: sec.Stats(), Total: total.Stats(), Hist: &hc}
			sec.Reset()
			if el >= nextWindow {
				iv := Interval{Start: nextWindow - cfg.Interval, End: el, Stats: window.Stats()}
				res.Intervals = append(res.Intervals, iv)
				u.Interval = &iv
				window.Reset()
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
		res.Intervals = append(res.Intervals, Interval{Start: nextWindow - cfg.Interval, End: res.Elapsed, Stats: window.Stats()})
	}
	res.Total, res.Hist = total.Stats(), total
	mu.Unlock()
	res.Counts = m.Counts()
	return res, runErr
}
