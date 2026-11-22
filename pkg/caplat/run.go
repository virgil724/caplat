package caplat

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/virgil724/caplat/capture"
	"github.com/virgil724/caplat/internal/barcode"
	"github.com/virgil724/caplat/displays"
	"github.com/virgil724/caplat/internal/measure"
	"github.com/virgil724/caplat/internal/present"
)

type captureStarter interface {
	Start(ctx context.Context, now func() int64, freq int64, onFrame func(capture.Frame)) (capture.Source, string, error)
}

type videoBackend interface {
	Displays() ([]Display, error)
	Devices(ctx context.Context, ffmpegBin string) ([]string, error)
	DevicesMF() ([]string, error)
	DevicesDShow() ([]string, error)
	DevicesV4L2() ([]string, error)
	Modes(ctx context.Context, ffmpegBin, device string) ([]Mode, error)
	ValidateBackend(backend string) error
	ValidatePresenter(presenter string) error
	ListCaptureDevices(ctx context.Context, cfg Config) ([]string, error)
	PrepareCapture(ctx context.Context, cfg Config, d Display, device string) (Setup, captureStarter, error)
	NewPresenter(presenter string) present.Presenter
}

type unsupportedVideoBackend struct{}

func (unsupportedVideoBackend) Displays() ([]Display, error)                   { return nil, ErrUnsupported }
func (unsupportedVideoBackend) Devices(context.Context, string) ([]string, error) {
	return nil, ErrUnsupported
}
func (unsupportedVideoBackend) DevicesMF() ([]string, error)    { return nil, ErrUnsupported }
func (unsupportedVideoBackend) DevicesDShow() ([]string, error) { return nil, ErrUnsupported }
func (unsupportedVideoBackend) DevicesV4L2() ([]string, error)  { return nil, ErrUnsupported }
func (unsupportedVideoBackend) Modes(context.Context, string, string) ([]Mode, error) {
	return nil, ErrUnsupported
}
func (unsupportedVideoBackend) ValidateBackend(string) error   { return ErrUnsupported }
func (unsupportedVideoBackend) ValidatePresenter(string) error { return ErrUnsupported }
func (unsupportedVideoBackend) ListCaptureDevices(context.Context, Config) ([]string, error) {
	return nil, ErrUnsupported
}
func (unsupportedVideoBackend) PrepareCapture(context.Context, Config, Display, string) (Setup, captureStarter, error) {
	return Setup{}, nil, ErrUnsupported
}
func (unsupportedVideoBackend) NewPresenter(string) present.Presenter { return nil }

type runDeps struct {
	vb                videoBackend
	now               func() int64
	freq              int64
	drainDelay        time.Duration
	stallTimeout      time.Duration // 0 disables stall detection
	startStallTimeout time.Duration // max wait from stream start before first frame
}

func defaultRunDeps() runDeps {
	return runDeps{
		vb:                newVideoBackend(),
		now:               capture.Now,
		freq:              capture.ClockFreq(),
		drainDelay:        1500 * time.Millisecond,
		stallTimeout:      3 * time.Second,
		startStallTimeout: 5 * time.Second,
	}
}

// Displays lists the desktop's outputs.
func Displays() ([]Display, error) { return newVideoBackend().Displays() }

// Devices lists video capture devices (through ffmpeg).
func Devices(ctx context.Context, ffmpegBin string) ([]string, error) {
	return newVideoBackend().Devices(ctx, ffmpegBin)
}

// DevicesMF lists video capture devices through Media Foundation (no
// ffmpeg needed).
func DevicesMF() ([]string, error) { return newVideoBackend().DevicesMF() }

// PickDevice chooses the capture card among capture devices ("" if that is
// ambiguous).
func PickDevice(devices []string) string { return displays.PickDevice(devices) }

// DevicesDShow lists DirectShow video capture devices in-process (no
// ffmpeg needed).
func DevicesDShow() ([]string, error) { return newVideoBackend().DevicesDShow() }

// DevicesV4L2 lists V4L2 video capture devices on Linux.
func DevicesV4L2() ([]string, error) { return newVideoBackend().DevicesV4L2() }

// Modes lists a device's capture modes.
func Modes(ctx context.Context, ffmpegBin, device string) ([]Mode, error) {
	return newVideoBackend().Modes(ctx, ffmpegBin, device)
}

type resolved struct {
	setup   Setup
	display displays.Display
	capW    int
	capH    int
	starter captureStarter
}

func resolve(ctx context.Context, cfg *Config, vb videoBackend) (resolved, error) {
	cfg.defaults()
	if err := vb.ValidateBackend(cfg.Backend); err != nil {
		return resolved{}, err
	}
	if err := vb.ValidatePresenter(cfg.Presenter); err != nil {
		return resolved{}, err
	}
	list, err := vb.Displays()
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
		devs, err := vb.ListCaptureDevices(ctx, *cfg)
		if err != nil {
			return resolved{}, err
		}
		if dev = displays.PickDevice(devs); dev == "" {
			return resolved{}, fmt.Errorf("choose a capture device with --device; found %q", devs)
		}
	}
	setup, starter, err := vb.PrepareCapture(ctx, *cfg, d, dev)
	if err != nil {
		return resolved{}, err
	}
	setup.Display = d.Name
	setup.DisplayName = d.FriendlyName
	setup.OutputMode = d.Mode()
	setup.Device = dev
	setup.Backend = cfg.Backend
	return resolved{
		setup:   setup,
		display: d,
		capW:    cfg.CaptureWidth,
		capH:    cfg.CaptureHeight,
		starter: starter,
	}, nil
}

// Prepare resolves the display, device and capture mode without measuring.
func Prepare(ctx context.Context, cfg Config) (Setup, error) {
	r, err := resolve(ctx, &cfg, newVideoBackend())
	return r.setup, err
}

// Run measures until cfg.Duration passes, ctx ends or the user aborts.
// updates (optional) receives an Update about once a second; sends never
// block, so give it a buffer. The result is returned even on ErrAborted.
func Run(ctx context.Context, cfg Config, updates chan<- Update) (Result, error) {
	return runWith(ctx, cfg, updates, defaultRunDeps())
}

func runWith(ctx context.Context, cfg Config, updates chan<- Update, deps runDeps) (Result, error) {
	r, err := resolve(ctx, &cfg, deps.vb)
	if err != nil {
		return Result{}, err
	}
	res := Result{Setup: r.setup}
	freq := deps.freq
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
	streamStart := time.Now()
	var gotFrame atomic.Bool
	var lastFrameOff atomic.Int64 // nanoseconds since streamStart
	onFrame := func(f capture.Frame) {
		lastFrameOff.Store(int64(time.Since(streamStart)))
		gotFrame.Store(true)
		code, ok := geo.Decode(f.Y, f.Stride)
		m.Captured(code, ok, f.QPC, f.DeviceQPC)
	}
	src, mode, err := r.starter.Start(runCtx, deps.now, freq, onFrame)
	if err != nil {
		return res, err
	}
	afterStart := time.Now()
	if mode != "" {
		res.Setup.CaptureMode = mode
	}
	defer src.Stop()

	checkStall := func(t0 time.Time) error {
		if deps.stallTimeout <= 0 {
			return nil
		}
		if gotFrame.Load() {
			if time.Since(streamStart)-time.Duration(lastFrameOff.Load()) >= deps.stallTimeout {
				return fmt.Errorf("no frames from %s for %s — %w (try replugging its USB / another port)", r.setup.Device, deps.stallTimeout, ErrStalled)
			}
			return nil
		}
		if !t0.IsZero() && time.Since(t0) >= deps.stallTimeout {
			return fmt.Errorf("no frames from %s for %s — %w (try replugging its USB / another port)", r.setup.Device, deps.stallTimeout, ErrStalled)
		}
		startTimeout := deps.startStallTimeout
		if startTimeout <= 0 {
			startTimeout = deps.stallTimeout
		}
		if time.Since(afterStart) >= startTimeout {
			return fmt.Errorf("no frames from %s for %s — %w (try replugging its USB / another port)", r.setup.Device, startTimeout, ErrStalled)
		}
		return nil
	}

	var stallCh <-chan time.Time
	if deps.stallTimeout > 0 {
		stallTick := time.NewTicker(min(250*time.Millisecond, max(deps.stallTimeout/4, 10*time.Millisecond)))
		defer stallTick.Stop()
		stallCh = stallTick.C
	}

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
	go func() { presErr <- present.Run(runCtx, pcfg, deps.vb.NewPresenter(cfg.Presenter)) }()

	// Wait for measuring to start (or an early failure).
	var t0 time.Time
waitStart:
	for {
		select {
		case t0 = <-started:
			break waitStart
		case err := <-presErr:
			if err == nil && ctx.Err() != nil {
				return res, ctx.Err()
			}
			return res, fmt.Errorf("presenter: %w", err)
		case err := <-src.Done():
			cancel()
			<-presErr
			return res, fmt.Errorf("capture: %w", err)
		case <-stallCh:
			if serr := checkStall(time.Time{}); serr != nil {
				cancel()
				<-presErr
				return res, serr
			}
		}
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
		case <-stallCh:
			if serr := checkStall(t0); serr != nil {
				cancel()
				<-presErr
				runErr = serr
				break loop
			}
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
	if runErr == nil && deps.drainDelay > 0 {
		time.Sleep(deps.drainDelay)
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
