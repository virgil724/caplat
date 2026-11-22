//go:build windows

package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"github.com/virgil724/caplat/capture/wasapi"
	"github.com/virgil724/caplat/internal/win"
)

// Endpoints lists the active endpoints' names; capture selects inputs.
func Endpoints(capture bool) ([]string, error) {
	if capture {
		return wasapi.Inputs()
	}
	return wasapi.Outputs()
}

// worker is a goroutine on a locked COM thread that runs until stopped.
type worker struct {
	done     chan error
	stop     chan struct{}
	stopOnce sync.Once
}

func newWorker() worker { return worker{done: make(chan error, 1), stop: make(chan struct{})} }

// Done delivers why the stream ended.
func (w *worker) Done() <-chan error { return w.done }

// Stop ends the stream.
func (w *worker) Stop() { w.stopOnce.Do(func() { close(w.stop) }) }

func (w *worker) stopped() bool {
	select {
	case <-w.stop:
		return true
	default:
		return false
	}
}

func (w *worker) start(run func(ready chan<- error) error) error {
	ready := make(chan error, 1)
	go func() {
		err := win.WithCOM(func() error { return run(ready) })
		select {
		case ready <- err:
		default:
		}
		w.done <- err
	}()
	return <-ready
}

// Player plays chirps in shared mode and reports when each was played,
// from IAudioClock's position: the device position and its QPC time.
type Player struct {
	worker
	Rate int
}

// StartPlayer starts playing; onEmit runs on the player thread.
func StartPlayer(cfg PlayerConfig, freq int64, onEmit func(Emitted)) (*Player, error) {
	p := &Player{worker: newWorker()}
	err := p.start(func(ready chan<- error) error { return p.run(cfg, freq, onEmit, ready) })
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Player) run(cfg PlayerConfig, freq int64, onEmit func(Emitted), ready chan<- error) error {
	dev, err := win.OpenEndpoint(win.ERender, cfg.Endpoint)
	if err != nil {
		return err
	}
	defer dev.Release()
	ac, err := win.NewAudioClient(dev, 300000, 0) // 30 ms
	if err != nil {
		return fmt.Errorf("%s: %w", cfg.Endpoint, err)
	}
	defer ac.Close()
	rc, err := ac.Service(&win.IID_IAudioRenderClient)
	if err != nil {
		return err
	}
	defer rc.Release()
	clk, err := ac.Service(&win.IID_IAudioClock)
	if err != nil {
		return err
	}
	defer clk.Release()
	clkFreq, err := win.ClockFrequency(clk)
	if err != nil || clkFreq == 0 {
		return fmt.Errorf("audio clock: %v", err)
	}

	f := ac.Format
	p.Rate = f.Rate
	chirp := cfg.Chirp(f.Rate)
	period := int64(cfg.Period * float64(f.Rate))
	first := int64(cfg.First * float64(f.Rate))
	jitter := int64(cfg.Jitter * float64(f.Rate))
	rng := rand.New(rand.NewPCG(uint64(freq), 0x6361706c6174))
	var written int64   // frames written so far
	var pending []int64 // chirps written, not yet played
	var sched []int64   // chirps whose samples are still being written
	nextChirp, nextN := first, 0

	fill := func(n int) error {
		buf, err := win.RenderBuffer(rc, n, f.BlockAlign)
		if err != nil {
			return err
		}
		end := written + int64(n)
		for nextChirp < end {
			sched = append(sched, nextChirp)
			pending = append(pending, nextChirp)
			nextChirp += period
			if jitter > 0 {
				nextChirp += rng.Int64N(jitter)
			}
		}
		for i := 0; i < n; i++ {
			idx, v := written+int64(i), 0.0
			for _, s := range sched {
				if idx >= s && idx < s+int64(len(chirp)) {
					v = cfg.Level * chirp[idx-s]
				}
			}
			for c := 0; c < f.Channels; c++ {
				putSample(buf[i*f.BlockAlign+c*f.Bits/8:], f, v)
			}
		}
		k := 0
		for _, s := range sched {
			if s+int64(len(chirp)) > end {
				sched[k] = s
				k++
			}
		}
		sched = sched[:k]
		written = end
		return win.RenderRelease(rc, n)
	}
	if err := fill(ac.Frames); err != nil {
		return err
	}
	if err := ac.Start(); err != nil {
		return err
	}
	defer ac.Stop()
	ready <- nil

	for !p.stopped() {
		ev, _ := windows.WaitForSingleObject(ac.Event, 200)
		if ev != windows.WAIT_OBJECT_0 {
			return errors.New("the audio output stopped signalling (device removed?)")
		}
		pad, err := ac.Padding()
		if err != nil {
			return err
		}
		if n := ac.Frames - pad; n > 0 {
			if err := fill(n); err != nil {
				return err
			}
		}
		pos, qpc, err := win.ClockPosition(clk)
		if err != nil {
			continue
		}
		played := float64(pos) / float64(clkFreq) * float64(f.Rate)
		for len(pending) > 0 && float64(pending[0]) <= played {
			at := hnsToQPC(int64(qpc), freq) + int64((float64(pending[0])-played)/float64(f.Rate)*float64(freq))
			onEmit(Emitted{N: nextN, QPC: at})
			nextN++
			pending = pending[1:]
		}
	}
	return nil
}

func putSample(b []byte, f win.WaveFormat, v float64) {
	switch {
	case f.Float:
		binary.LittleEndian.PutUint32(b, math.Float32bits(float32(v)))
	case f.Bits == 16:
		binary.LittleEndian.PutUint16(b, uint16(int16(v*32767)))
	case f.Bits == 24:
		x := int32(v * 8388607)
		b[0], b[1], b[2] = byte(x), byte(x>>8), byte(x>>16)
	case f.Bits == 32:
		binary.LittleEndian.PutUint32(b, uint32(int32(v*2147483647)))
	}
}

// WASAPICapture adapts the public WASAPI capture to mono samples.
type WASAPICapture struct{ *wasapi.Capture }

// Rate is the capture's sample rate.
func (c *WASAPICapture) Rate() int { return c.Capture.Rate }

// StartWASAPI starts capturing endpoint, asking for an engine period of
// period (0 = default); onSamples runs on the capture thread.
func StartWASAPI(endpoint string, period time.Duration, exclusive bool, now func() int64, freq int64, onSamples OnSamples) (*WASAPICapture, error) {
	var mono []float64
	c, err := wasapi.Start(wasapi.Config{Endpoint: endpoint, Period: period, Exclusive: exclusive}, now, freq, func(p wasapi.Packet) {
		ch := len(p.Samples) / max(p.Frames, 1)
		mono = mono[:0]
		for i := 0; i < p.Frames; i++ {
			mono = append(mono, float64(p.Samples[i*ch]))
		}
		onSamples(mono, Packet{Start: p.Start, Frames: p.Frames, DeviceQPC: p.DeviceQPC, Arrive: p.Arrive})
	})
	if err != nil {
		return nil, err
	}
	return &WASAPICapture{c}, nil
}

func hnsToQPC(hns, freq int64) int64 { return wasapi.HNSToQPC(hns, freq) }
