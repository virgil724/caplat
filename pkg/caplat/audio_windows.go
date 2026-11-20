//go:build windows

package caplat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/virgil724/caplat/internal/audio"
	"github.com/virgil724/caplat/internal/displays"
	"github.com/virgil724/caplat/internal/measure"
	"github.com/virgil724/caplat/internal/win"
)

// The test signal.
const (
	chirpMS     = 10
	chirpF0     = 1000
	chirpF1     = 8000
	chirpPeriod = 0.5 // seconds; latency must stay below 0.45 s to pair
	chirpFirst  = 1.0
	chirpLevel  = 0.5
)

// AudioOutputs and AudioInputs list the active audio endpoints.
func AudioOutputs() ([]string, error) { return audio.Endpoints(false) }
func AudioInputs() ([]string, error)  { return audio.Endpoints(true) }

func pickEndpoint(names []string, want, what string) (string, error) {
	if want != "" {
		return audio.FindName(names, want)
	}
	if n := displays.PickDevice(names); n != "" {
		return n, nil
	}
	return "", fmt.Errorf("choose the audio %s; found %q", what, names)
}

type audioSource interface {
	Done() <-chan error
	Stop()
	Rate() int
}

type detection struct {
	dev, arr int64 // QPC ticks; dev 0 if unknown
}

// RunAudio measures until cfg.Duration passes or ctx ends.
func RunAudio(ctx context.Context, cfg AudioConfig) (AudioResult, error) {
	if cfg.Backend == "" {
		cfg.Backend = AudioWASAPI
	}
	if cfg.Backend != AudioWASAPI && cfg.Backend != AudioFFmpeg {
		return AudioResult{}, fmt.Errorf("unknown audio backend %q (want %s or %s)", cfg.Backend, AudioWASAPI, AudioFFmpeg)
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Second
	}
	outs, err := AudioOutputs()
	if err != nil {
		return AudioResult{}, err
	}
	out, err := pickEndpoint(outs, cfg.Output, "output (--output)")
	if err != nil {
		return AudioResult{}, err
	}
	ins, err := AudioInputs()
	if err != nil {
		return AudioResult{}, err
	}
	in, err := pickEndpoint(ins, cfg.Input, "input (--input)")
	if err != nil {
		return AudioResult{}, err
	}
	res := AudioResult{Output: out, Input: in, Backend: cfg.Backend}
	freq := win.QPCFrequency()
	toMS := func(ticks int64) float64 { return float64(ticks) * 1000 / float64(freq) }

	var (
		mu             sync.Mutex
		emitted        []audio.Emitted
		dets           []detection
		packets        []audio.Packet
		det            atomic.Pointer[audio.Detector]
		rate           int64
		delTot, delWin measure.Histogram
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

	var src audioSource
	switch cfg.Backend {
	case AudioWASAPI:
		c, err := audio.StartWASAPI(in, win.QPC, freq, func(x []float64, p audio.Packet) {
			if det.Load() != nil {
				onSamples(x, p)
			}
		})
		if err != nil {
			return res, err
		}
		src = c
	case AudioFFmpeg:
		fc := audio.FFmpegConfig{FFmpeg: cfg.FFmpeg, Device: in, LowLatency: cfg.Preset != PresetDefault}
		res.FFmpegArgs = audio.FFmpegArgs(fc)
		c, err := audio.StartFFmpeg(ctx, fc, win.QPC, func(x []float64, p audio.Packet) {
			if det.Load() != nil {
				onSamples(x, p)
			}
		})
		if err != nil {
			return res, err
		}
		src = c
	}
	defer src.Stop()
	// The rate is known once the capture runs; publishing the detector
	// starts detection on the capture thread.
	rate = int64(src.Rate())
	d := audio.NewDetector(audio.Chirp(src.Rate(), src.Rate()*chirpMS/1000, chirpF0, chirpF1))
	d.OnHit = onHit
	det.Store(d)

	pl, err := audio.StartPlayer(audio.PlayerConfig{
		Endpoint: out, Period: chirpPeriod, First: chirpFirst, Level: chirpLevel,
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
	res.OutputFormat = fmt.Sprintf("%d Hz", pl.Rate)

	// Pair detections with the chirp played last before them.
	maxLag := int64(0.45 * float64(freq))
	match := func(final bool) {
		cutoff := win.QPC() - int64(0.3*float64(freq))
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
				if e.QPC <= ref && ref-e.QPC < maxLag {
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
		now := win.QPC()
		j := 0
		for _, e := range emitted {
			if now-e.QPC < 2*maxLag {
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
	nextWin := cfg.Interval
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
			if cfg.Duration > 0 && el >= cfg.Duration {
				break loop
			}
		}
	}
	pl.Stop()
	time.Sleep(600 * time.Millisecond) // let the last chirps arrive
	src.Stop()
	match(true)
	res.Elapsed = time.Since(t0)
	res.Delivered, res.Device = delTot.Stats(), devTot.Stats()
	if res.Matched == 0 && runErr == nil {
		runErr = errors.New("no chirp came back: is the output's audio reaching the card, and the right input chosen?")
	}
	return res, runErr
}
