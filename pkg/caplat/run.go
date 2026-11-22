package caplat

import (
	"bufio"
	"context"
	"errors"
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
	if cfg.Repeat > 1 && cfg.Duration <= 0 {
		return resolved{}, errors.New("--repeat > 1 requires a positive --duration")
	}
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

func addCounts(a, b Counts) Counts {
	return Counts{
		OnlyVblank:  a.OnlyVblank + b.OnlyVblank,
		OnlyCapture: a.OnlyCapture + b.OnlyCapture,
		DecodeFail:  a.DecodeFail + b.DecodeFail,
		Captured:    a.Captured + b.Captured,
		Mismatched:  a.Mismatched + b.Mismatched,
	}
}

func runWith(ctx context.Context, cfg Config, updates chan<- Update, deps runDeps) (Result, error) {
	r, err := resolve(ctx, &cfg, deps.vb)
	if err != nil {
		return Result{}, err
	}
	repeat := cfg.Repeat
	res := Result{Setup: r.setup}
	freq := deps.freq
	geo := barcode.Geometry{OutW: r.display.Width, OutH: r.display.Height, CapW: r.capW, CapH: r.capH}

	var (
		mu          sync.Mutex
		aggTotal    measure.Histogram
		aggDevTotal measure.Histogram
		csv         *bufio.Writer
	)
	if cfg.CSV != nil {
		csv = bufio.NewWriter(cfg.CSV)
		if repeat > 1 {
			fmt.Fprintf(csv, "# caplat display=%s (%s) output=%s device=%q backend=%s capture=%s preset=%s repeat=%d\n",
				r.setup.Display, r.setup.DisplayName, r.setup.OutputMode, r.setup.Device, r.setup.Backend, r.setup.CaptureMode, r.setup.Preset, repeat)
			fmt.Fprintln(csv, "run,frame,vblank_qpc,arrive_qpc,latency_ms,device_qpc,device_ms")
		} else {
			fmt.Fprintf(csv, "# caplat display=%s (%s) output=%s device=%q backend=%s capture=%s preset=%s\n",
				r.setup.Display, r.setup.DisplayName, r.setup.OutputMode, r.setup.Device, r.setup.Backend, r.setup.CaptureMode, r.setup.Preset)
			fmt.Fprintln(csv, "frame,vblank_qpc,arrive_qpc,latency_ms,device_qpc,device_ms")
		}
		defer csv.Flush()
	}

	hz := r.display.RefreshHz()
	if hz <= 0 {
		hz = 60
	}
	warmFrames := int(cfg.Warmup.Seconds() * hz)
	var measureFrames uint64
	if cfg.Duration > 0 {
		measureFrames = uint64(cfg.Duration.Seconds() * hz)
	}

	presCtx, presCancel := context.WithCancel(ctx)
	defer presCancel()
	segs := make(chan present.Segment)
	sessionDone := make(chan struct{})
	var sessionErr error
	go func() {
		sessionErr = present.RunSession(presCtx, r.display, deps.vb.NewPresenter(cfg.Presenter), segs)
		close(sessionDone)
	}()
	var closeSegsOnce sync.Once
	closeSegs := func() {
		closeSegsOnce.Do(func() { close(segs) })
	}
	defer func() {
		presCancel()
		closeSegs()
		<-sessionDone
	}()

	var runErr error
	for runIdx := 1; runIdx <= repeat; runIdx++ {
		var (
			runTotal, runDevTotal measure.Histogram
			sec, window, devWin   measure.Histogram
		)
		m := measure.NewMatcher(freq, func(s measure.Sample) {
			mu.Lock()
			runTotal.Add(s.LatencyMS)
			aggTotal.Add(s.LatencyMS)
			sec.Add(s.LatencyMS)
			window.Add(s.LatencyMS)
			if !math.IsNaN(s.DeviceMS) {
				runDevTotal.Add(s.DeviceMS)
				aggDevTotal.Add(s.DeviceMS)
				devWin.Add(s.DeviceMS)
			}
			if csv != nil {
				dev := ""
				if !math.IsNaN(s.DeviceMS) {
					dev = fmt.Sprintf("%.3f", s.DeviceMS)
				}
				if repeat > 1 {
					fmt.Fprintf(csv, "%d,%d,%d,%d,%.3f,%d,%s\n", runIdx, s.Frame, s.VblankQPC, s.ArriveQPC, s.LatencyMS, s.DeviceQPC, dev)
				} else {
					fmt.Fprintf(csv, "%d,%d,%d,%.3f,%d,%s\n", s.Frame, s.VblankQPC, s.ArriveQPC, s.LatencyMS, s.DeviceQPC, dev)
				}
			}
			mu.Unlock()
		})
		m.Settle = 10

		segCtx, segCancel := context.WithCancel(ctx)
		streamStart := time.Now()
		var gotFrame atomic.Bool
		var lastFrameOff atomic.Int64 // nanoseconds since streamStart
		onFrame := func(f capture.Frame) {
			lastFrameOff.Store(int64(time.Since(streamStart)))
			gotFrame.Store(true)
			code, ok := geo.Decode(f.Y, f.Stride)
			m.Captured(code, ok, f.QPC, f.DeviceQPC)
		}
		src, mode, err := r.starter.Start(segCtx, deps.now, freq, onFrame)
		if err != nil {
			segCancel()
			runErr = err
			break
		}
		afterStart := time.Now()
		if mode != "" {
			res.Setup.CaptureMode = mode
		}

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

		var stallTick *time.Ticker
		var stallCh <-chan time.Time
		if deps.stallTimeout > 0 {
			stallTick = time.NewTicker(min(250*time.Millisecond, max(deps.stallTimeout/4, 10*time.Millisecond)))
			stallCh = stallTick.C
		}

		trailing := 0
		if runIdx < repeat {
			trailing = 3
		}
		started := make(chan time.Time, 1)
		segDone := make(chan error, 1)
		seg := present.Segment{
			Ctx:        segCtx,
			WarmFrames: warmFrames,
			Frames:     measureFrames,
			Trailing:   trailing,
			OnVblank:   m.Vblank,
			OnStart:    func() { started <- time.Now() },
			Done:       segDone,
		}

		sent := false
		for !sent && runErr == nil {
			select {
			case segs <- seg:
				sent = true
			case <-sessionDone:
				if sessionErr == nil && ctx.Err() != nil {
					runErr = ctx.Err()
				} else {
					runErr = fmt.Errorf("presenter: %w", sessionErr)
				}
			case err := <-src.Done():
				runErr = fmt.Errorf("capture: %w", err)
			case <-stallCh:
				if serr := checkStall(time.Time{}); serr != nil {
					runErr = serr
				}
			}
		}
		if runErr != nil {
			if stallTick != nil {
				stallTick.Stop()
			}
			segCancel()
			src.Stop()
			break
		}

		var t0 time.Time
		segEnded := false
	waitStart:
		for {
			select {
			case t0 = <-started:
				break waitStart
			case err := <-segDone:
				segEnded = true
				if err == nil && ctx.Err() != nil {
					runErr = ctx.Err()
				} else {
					runErr = fmt.Errorf("presenter: %w", err)
				}
				break waitStart
			case err := <-src.Done():
				runErr = fmt.Errorf("capture: %w", err)
				break waitStart
			case <-stallCh:
				if serr := checkStall(time.Time{}); serr != nil {
					runErr = serr
					break waitStart
				}
			}
		}

		if runErr != nil {
			if stallTick != nil {
				stallTick.Stop()
			}
			segCancel()
			if !segEnded {
				<-segDone
			}
			src.Stop()
			break
		}

		tick := time.NewTicker(time.Second)
		nextWindow := cfg.Interval
	loop:
		for {
			select {
			case err := <-segDone:
				runErr = err
				break loop
			case err := <-src.Done():
				segCancel()
				<-segDone
				runErr = fmt.Errorf("capture stopped early: %w", err)
				break loop
			case <-stallCh:
				if serr := checkStall(t0); serr != nil {
					segCancel()
					<-segDone
					runErr = serr
					break loop
				}
			case <-tick.C:
				el := time.Since(t0)
				mu.Lock()
				hc := aggTotal
				u := Update{Elapsed: res.Elapsed + el, Second: sec.Stats(), Total: aggTotal.Stats(), Hist: &hc, Device: aggDevTotal.Stats()}
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
				u.Counts = addCounts(res.Counts, m.Counts())
				if updates != nil {
					select {
					case updates <- u:
					default:
					}
				}
			}
		}
		tick.Stop()
		if stallTick != nil {
			stallTick.Stop()
		}

		// Let the last frames arrive, then stop the capture.
		m.PresenterStopped()
		if runIdx == repeat && runErr == nil {
			closeSegs()
			<-sessionDone
		}
		if runErr == nil && deps.drainDelay > 0 {
			select {
			case <-time.After(deps.drainDelay):
			case <-ctx.Done():
			}
		}
		segCancel()
		src.Stop()
		if runIdx < repeat && runErr == nil && ctx.Err() == nil {
			select {
			case <-src.Done():
			case <-ctx.Done():
			}
		}
		m.Flush()
		runElapsed := time.Since(t0)
		mu.Lock()
		res.Elapsed += runElapsed
		if window.N > 0 {
			res.Intervals = append(res.Intervals, Interval{Start: nextWindow - cfg.Interval, End: runElapsed, Stats: window.Stats(), Device: devWin.Stats()})
		}
		rr := RunResult{
			Run:     runIdx,
			Total:   runTotal.Stats(),
			Device:  runDevTotal.Stats(),
			Counts:  m.Counts(),
			Hist:    runTotal,
			Elapsed: runElapsed,
		}
		mu.Unlock()
		res.Counts = addCounts(res.Counts, rr.Counts)
		if repeat > 1 {
			res.Runs = append(res.Runs, rr)
			if cfg.OnRun != nil {
				cfg.OnRun(rr)
			}
		}
		if runErr != nil || ctx.Err() != nil {
			break
		}
	}

	mu.Lock()
	res.Total, res.Hist, res.Device = aggTotal.Stats(), aggTotal, aggDevTotal.Stats()
	mu.Unlock()
	return res, runErr
}
