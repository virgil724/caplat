package caplat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/virgil724/caplat/capture"
	"github.com/virgil724/caplat/capture/alsa"
	"github.com/virgil724/caplat/displays"
	"github.com/virgil724/caplat/internal/audio"
	"github.com/virgil724/caplat/internal/measure"
)

// The test signal.
const (
	chirpMS     = 10
	chirpF0     = 1000
	chirpF1     = 8000
	chirpPeriod = 0.5 // seconds; latency must stay below 0.45 s to pair
	chirpFirst  = 1.0
	chirpLevel  = 0.5
	// chirpJitter spreads chirps over two 10 ms capture packet periods.
	chirpJitter = 0.02
)

type audioSource interface {
	Done() <-chan error
	Stop()
	Rate() int
}

type audioPlayer interface {
	Done() <-chan error
	Stop()
}

type audioBackend interface {
	ValidateBackend(backend string) error
	Outputs() ([]string, error)
	Inputs() ([]string, error)
	StartCapture(ctx context.Context, cfg AudioConfig, input string, now func() int64, freq int64, onSamples audio.OnSamples) (audioSource, time.Duration, []string, error)
	StartPlayer(cfg audio.PlayerConfig, freq int64, onEmit func(audio.Emitted)) (audioPlayer, int, error)
}

type unsupportedAudioBackend struct{}

func (unsupportedAudioBackend) ValidateBackend(string) error    { return ErrUnsupported }
func (unsupportedAudioBackend) Outputs() ([]string, error)      { return nil, ErrUnsupported }
func (unsupportedAudioBackend) Inputs() ([]string, error)       { return nil, ErrUnsupported }
func (unsupportedAudioBackend) StartCapture(context.Context, AudioConfig, string, func() int64, int64, audio.OnSamples) (audioSource, time.Duration, []string, error) {
	return nil, 0, nil, ErrUnsupported
}
func (unsupportedAudioBackend) StartPlayer(audio.PlayerConfig, int64, func(audio.Emitted)) (audioPlayer, int, error) {
	return nil, 0, ErrUnsupported
}

type audioDeps struct {
	ab         audioBackend
	now        func() int64
	freq       int64
	drainDelay time.Duration
}

func defaultAudioDeps() audioDeps {
	return audioDeps{
		ab:         newAudioBackend(),
		now:        capture.Now,
		freq:       capture.ClockFreq(),
		drainDelay: 600 * time.Millisecond,
	}
}

// AudioOutputs lists the active audio render endpoints.
func AudioOutputs() ([]string, error) { return newAudioBackend().Outputs() }

// AudioInputs lists the active audio capture endpoints.
func AudioInputs() ([]string, error) { return newAudioBackend().Inputs() }

func isLinuxAudioBackend(backend string) bool {
	return backend == AudioALSA || (backend == AudioFFmpeg && runtime.GOOS == "linux")
}

func pickEndpoint(names []string, want, what, backend string) (string, error) {
	if want != "" {
		if n, err := audio.FindName(names, want); err == nil {
			return n, nil
		} else if isLinuxAudioBackend(backend) && alsa.IsDirectPCM(want) {
			return want, nil
		} else {
			return "", err
		}
	}
	if n := displays.PickDevice(names); n != "" {
		return n, nil
	}
	return "", fmt.Errorf("choose the audio %s; found %q", what, names)
}

type detection struct {
	dev, arr int64 // platform monotonic ticks; dev 0 if unknown
}

// RunAudio measures until cfg.Duration passes or ctx ends.
func RunAudio(ctx context.Context, cfg AudioConfig) (AudioResult, error) {
	return runAudioWith(ctx, cfg, defaultAudioDeps())
}

func runAudioWith(ctx context.Context, cfg AudioConfig, deps audioDeps) (AudioResult, error) {
	if cfg.Backend == "" {
		cfg.Backend = DefaultAudioBackend()
	}
	if err := deps.ab.ValidateBackend(cfg.Backend); err != nil {
		return AudioResult{}, err
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Second
	}
	outs, err := deps.ab.Outputs()
	if err != nil && !(isLinuxAudioBackend(cfg.Backend) && alsa.IsDirectPCM(cfg.Output)) {
		return AudioResult{}, err
	}
	out, err := pickEndpoint(outs, cfg.Output, "output (--output)", cfg.Backend)
	if err != nil {
		return AudioResult{}, err
	}
	ins, err := deps.ab.Inputs()
	if err != nil && !(isLinuxAudioBackend(cfg.Backend) && alsa.IsDirectPCM(cfg.Input)) {
		return AudioResult{}, err
	}
	in, err := pickEndpoint(ins, cfg.Input, "input (--input)", cfg.Backend)
	if err != nil {
		return AudioResult{}, err
	}
	res := AudioResult{Output: out, Input: in, Backend: cfg.Backend}
	freq := deps.freq
	toMS := func(ticks int64) float64 { return float64(ticks) * 1000 / float64(freq) }

	var (
		mu             sync.Mutex
		emitted        []audio.Emitted
		dets           []detection
		packets        []audio.Packet
		det            atomic.Pointer[audio.Detector]
		rate           int64
		delTot, delWin measure.Histogram
		delSec         measure.Histogram
		devTot, devWin measure.Histogram
		csv            *bufio.Writer
	)
	if cfg.CSV != nil {
		csv = bufio.NewWriter(cfg.CSV)
		defer csv.Flush()
	}

	// Capture: find chirps; note when each one's first sample was captured
	// (driver) and when caplat had it.
	onSamples := func(x []float64, p audio.Packet) {
		packets = append(packets, p)
		if len(packets) > 400 {
			packets = append(packets[:0], packets[len(packets)-200:]...)
		}
		det.Load().Push(x)
	}
	onHit := func(start int64, score float64) {
		for i := len(packets) - 1; i >= 0; i-- {
			p := packets[i]
			if start < p.Start || start >= p.Start+int64(p.Frames) {
				continue
			}
			d := detection{arr: p.Arrive}
			if p.DeviceQPC != 0 {
				d.dev = p.DeviceQPC + (start-p.Start)*freq/rate
			}
			mu.Lock()
			dets = append(dets, d)
			res.Detected++
			mu.Unlock()
			return
		}
	}

	src, period, ffArgs, err := deps.ab.StartCapture(ctx, cfg, in, deps.now, freq, func(x []float64, p audio.Packet) {
		if det.Load() != nil {
			onSamples(x, p)
		}
	})
	res.InputPeriod = period
	res.FFmpegArgs = ffArgs
	if err != nil {
		return res, err
	}
	defer src.Stop()
	if sw, ok := src.(interface{ UsingSoftwareMixer() bool }); ok && sw.UsingSoftwareMixer() {
		res.SoftwareMixer = true
	}
	// The rate is known once the capture runs; publishing the detector
	// starts detection on the capture thread.
	rate = int64(src.Rate())
	d := audio.NewDetector(audio.Chirp(src.Rate(), src.Rate()*chirpMS/1000, chirpF0, chirpF1))
	d.OnHit = onHit
	det.Store(d)

	pl, outRate, err := deps.ab.StartPlayer(audio.PlayerConfig{
		Endpoint: out, Period: chirpPeriod, First: chirpFirst, Level: chirpLevel, Jitter: chirpJitter,
		Chirp: func(rate int) []float64 { return audio.Chirp(rate, rate*chirpMS/1000, chirpF0, chirpF1) },
	}, freq, func(e audio.Emitted) {
		mu.Lock()
		emitted = append(emitted, e)
		res.Emitted++
		mu.Unlock()
	})
	if err != nil {
		return res, err
	}
	defer pl.Stop()
	if sw, ok := pl.(interface{ UsingSoftwareMixer() bool }); ok && sw.UsingSoftwareMixer() {
		res.SoftwareMixer = true
	}
	res.OutputFormat = fmt.Sprintf("%d Hz", outRate)

	// Pair detections with the chirp played last before them.
	maxLag := int64(0.45 * float64(freq))
	// A driver capture timestamp can read slightly before the player's
	// clock reading on a ~zero-latency path (e.g. snd-aloop), because the
	// two are sampled at different points; allow a small negative slack
	// instead of silently dropping the detection.
	devSlack := int64(0.005 * float64(freq))
	match := func(final bool) {
		cutoff := deps.now() - int64(0.3*float64(freq))
		mu.Lock()
		defer mu.Unlock()
		k := 0
		for _, dt := range dets {
			if !final && dt.arr > cutoff {
				dets[k] = dt
				k++
				continue
			}
			ref := dt.arr
			if dt.dev != 0 {
				ref = dt.dev
			}
			best := -1
			for i, e := range emitted {
				if e.QPC <= ref+devSlack && ref-e.QPC < maxLag {
					best = i
				}
			}
			if best < 0 {
				continue
			}
			e := emitted[best]
			emitted = append(emitted[:best], emitted[best+1:]...)
			del := toMS(dt.arr - e.QPC)
			delTot.Add(del)
			delWin.Add(del)
			delSec.Add(del)
			devStr := ""
			if dt.dev != 0 {
				dv := toMS(dt.dev - e.QPC)
				devTot.Add(dv)
				devWin.Add(dv)
				devStr = fmt.Sprintf("%.3f", dv)
			}
			res.Matched++
			if csv != nil {
				fmt.Fprintf(csv, "%d,%d,%d,%.3f,%d,%s\n", e.N, e.QPC, dt.arr, del, dt.dev, devStr)
			}
		}
		dets = dets[:k]
		// Forget chirps too old to be matched.
		nowTick := deps.now()
		j := 0
		for _, e := range emitted {
			if nowTick-e.QPC < 2*maxLag {
				emitted[j] = e
				j++
			}
		}
		emitted = emitted[:j]
	}
	if csv != nil {
		fmt.Fprintf(csv, "# caplat audio output=%q input=%q backend=%s\n", out, in, cfg.Backend)
		fmt.Fprintln(csv, "chirp,played_qpc,arrive_qpc,delivered_ms,device_qpc,device_ms")
	}

	t0 := time.Now()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	nextWin, nextUpdate := cfg.Interval, time.Second
	var runErr error
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case err := <-src.Done():
			runErr = fmt.Errorf("audio capture stopped: %v", err)
			break loop
		case err := <-pl.Done():
			runErr = fmt.Errorf("audio output stopped: %v", err)
			break loop
		case <-tick.C:
			match(false)
			el := time.Since(t0)
			if el >= nextWin {
				mu.Lock()
				iv := AudioInterval{Start: nextWin - cfg.Interval, End: el, Delivered: delWin.Stats(), Device: devWin.Stats()}
				delWin.Reset()
				devWin.Reset()
				mu.Unlock()
				res.Intervals = append(res.Intervals, iv)
				if cfg.OnInterval != nil {
					cfg.OnInterval(iv)
				}
				nextWin += cfg.Interval
			}
			if cfg.OnUpdate != nil && el >= nextUpdate {
				nextUpdate += time.Second
				mu.Lock()
				hc := delTot
				u := AudioUpdate{Elapsed: el, Second: delSec.Stats(), Total: delTot.Stats(), Device: devTot.Stats(), Hist: &hc,
					Emitted: res.Emitted, Detected: res.Detected, Matched: res.Matched}
				delSec.Reset()
				mu.Unlock()
				cfg.OnUpdate(u)
			}
			if cfg.Duration > 0 && el >= cfg.Duration {
				break loop
			}
		}
	}
	pl.Stop()
	if deps.drainDelay > 0 {
		time.Sleep(deps.drainDelay) // let the last chirps arrive
	}
	src.Stop()
	match(true)
	res.Elapsed = time.Since(t0)
	mu.Lock()
	res.Delivered, res.Device = delTot.Stats(), devTot.Stats()
	matched := res.Matched
	mu.Unlock()
	if matched == 0 && runErr == nil {
		runErr = errors.New("no chirp came back: is the output's audio reaching the card, and the right input chosen?")
	}
	return res, runErr
}
