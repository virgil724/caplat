//go:build linux

package audio

import (
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"time"

	"github.com/virgil724/caplat/capture"
	"github.com/virgil724/caplat/capture/alsa"
)

// ALSACapture adapts the public ALSA capture to mono samples.
type ALSACapture struct {
	*alsa.Capture
}

// Rate is the capture's sample rate.
func (c *ALSACapture) Rate() int { return c.Capture.Rate }

// UsingSoftwareMixer reports whether ALSA fell back to a software mixer (e.g. pipewire/default).
func (c *ALSACapture) UsingSoftwareMixer() bool { return c.Capture.SoftwareMixer }

// StartALSA starts capturing endpoint via ALSA (with pipewire/default fallback
// unless exclusive is true); onSamples runs on the capture goroutine.
func StartALSA(endpoint string, period time.Duration, exclusive bool, now func() int64, freq int64, onSamples OnSamples) (*ALSACapture, error) {
	var mono []float64
	c, err := alsa.Start(alsa.Config{
		Device:          endpoint,
		Period:          period,
		FallbackDefault: !exclusive,
	}, now, freq, func(p alsa.Packet) {
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
	if c.SoftwareMixer {
		fmt.Fprintf(os.Stderr, "caplat: ALSA capture %q was busy; fell back to software mixer %q (latency includes PipeWire/mixer buffering)\n", endpoint, c.ActualDevice)
	}
	return &ALSACapture{c}, nil
}

// ALSAPlayer plays chirps over ALSA and reports each chirp's departure timestamp
// on CLOCK_MONOTONIC via snd_pcm_status htstamp + delay.
type ALSAPlayer struct {
	Rate    int
	swMixer bool

	done     chan error
	stop     chan struct{}
	stopOnce sync.Once
}

// UsingSoftwareMixer reports whether playback fell back to a software mixer.
func (p *ALSAPlayer) UsingSoftwareMixer() bool { return p.swMixer }

// Done delivers why playback ended.
func (p *ALSAPlayer) Done() <-chan error { return p.done }

// Stop ends playback.
func (p *ALSAPlayer) Stop() { p.stopOnce.Do(func() { close(p.stop) }) }

func (p *ALSAPlayer) stopped() bool {
	select {
	case <-p.stop:
		return true
	default:
		return false
	}
}

// StartALSAPlayer opens cfg.Endpoint via ALSA and starts emitting chirps.
func StartALSAPlayer(cfg PlayerConfig, freq int64, onEmit func(Emitted)) (*ALSAPlayer, error) {
	p := &ALSAPlayer{done: make(chan error, 1), stop: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		err := p.run(cfg, freq, onEmit, ready)
		select {
		case ready <- err:
		default:
		}
		p.done <- err
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	return p, nil
}

func (p *ALSAPlayer) run(cfg PlayerConfig, freq int64, onEmit func(Emitted), ready chan<- error) error {
	pb, err := alsa.OpenPlayback(alsa.PlaybackConfig{
		Device:          cfg.Endpoint,
		Rate:            48000,
		Channels:        2,
		Period:          10 * time.Millisecond,
		FallbackDefault: true,
	})
	if err != nil {
		return err
	}
	defer pb.Close()

	if pb.SoftwareMixer {
		p.swMixer = true
		fmt.Fprintf(os.Stderr, "caplat: ALSA output %q was busy; fell back to software mixer %q\n", cfg.Endpoint, pb.ActualDevice)
	}

	p.Rate = pb.Rate
	chirp := cfg.Chirp(pb.Rate)
	period := int64(cfg.Period * float64(pb.Rate))
	first := int64(cfg.First * float64(pb.Rate))
	jitter := int64(cfg.Jitter * float64(pb.Rate))
	rng := rand.New(rand.NewPCG(uint64(freq), 0x6361706c6174))

	var written int64
	var pending []int64
	var sched []int64
	nextChirp, nextN := first, 0

	chunkFrames := max(pb.PeriodFrames, pb.Rate/100)
	buf := make([]float32, chunkFrames*pb.Channels)

	fillAndWrite := func(n int) error {
		need := n * pb.Channels
		if cap(buf) < need {
			buf = make([]float32, need)
		}
		pcm := buf[:need]
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
			for c := 0; c < pb.Channels; c++ {
				pcm[i*pb.Channels+c] = float32(v)
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

		for off := 0; off < n; {
			w, err := pb.WriteFrames(pcm[off*pb.Channels:])
			if err != nil {
				return err
			}
			if w == 0 {
				break
			}
			off += w
			written += int64(w)
		}
		return nil
	}

	// Prime the buffer before start_threshold triggers playback.
	prime := chunkFrames
	if pb.BufferFrames > 0 {
		prime = min(pb.BufferFrames, max(pb.PeriodFrames*2, chunkFrames))
	}
	if err := fillAndWrite(prime); err != nil {
		return err
	}
	ready <- nil

	for !p.stopped() {
		ok, err := pb.Wait(200)
		if err != nil {
			return err
		}
		if p.stopped() {
			break
		}
		if ok {
			if err := fillAndWrite(chunkFrames); err != nil {
				return err
			}
		}
		htNS, delay, err := pb.Status()
		if err != nil {
			continue
		}
		nowTicks := capture.Now()
		played := written - delay
		for len(pending) > 0 && pending[0] <= played {
			at := alsa.PlaybackChirpTicks(htNS, nowTicks, delay, written, pending[0], pb.Rate, freq)
			onEmit(Emitted{N: nextN, QPC: at})
			nextN++
			pending = pending[1:]
		}
	}
	return nil
}
