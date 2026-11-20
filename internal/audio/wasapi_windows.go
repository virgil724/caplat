//go:build windows

package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/sys/windows"

	"github.com/virgil724/caplat/internal/win"
)

func withCOM(f func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := win.COInitMultithreaded(); err != nil {
		return fmt.Errorf("COM: %w", err)
	}
	defer win.COUninit()
	return f()
}

// Endpoints lists the active endpoints' names; capture selects inputs.
func Endpoints(capture bool) ([]string, error) {
	flow := win.ERender
	if capture {
		flow = win.ECapture
	}
	var names []string
	err := withCOM(func() error {
		eps, err := win.AudioEndpoints(flow)
		for _, e := range eps {
			names = append(names, e.Name)
			e.Device.Release()
		}
		return err
	})
	return names, err
}

// FindName returns the one name equal to want, or else containing it
// (case-insensitive).
func FindName(names []string, want string) (string, error) {
	var hits []string
	for _, n := range names {
		if strings.EqualFold(n, want) {
			return n, nil
		}
		if strings.Contains(strings.ToLower(n), strings.ToLower(want)) {
			hits = append(hits, n)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return "", fmt.Errorf("no audio endpoint matches %q; have %q", want, names)
	}
	return "", fmt.Errorf("%q matches several endpoints: %q", want, hits)
}

func openEndpoint(flow int, name string) (win.Object, error) {
	eps, err := win.AudioEndpoints(flow)
	if err != nil {
		return 0, err
	}
	var dev win.Object
	for _, e := range eps {
		if dev == 0 && e.Name == name {
			dev = e.Device
			continue
		}
		e.Device.Release()
	}
	if dev == 0 {
		return 0, fmt.Errorf("audio endpoint %q not found", name)
	}
	return dev, nil
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
		err := withCOM(func() error { return run(ready) })
		select {
		case ready <- err:
		default:
		}
		w.done <- err
	}()
	return <-ready
}

// Emitted is one chirp as it left the output: its first sample's QPC time.
type Emitted struct {
	N   int   // chirp number, from 0
	QPC int64 // when its first sample was played, in QPC ticks
}

// PlayerConfig describes the test signal.
type PlayerConfig struct {
	Endpoint string
	Chirp    func(rate int) []float64 // the chirp at the endpoint's rate
	Period   float64                  // seconds between chirps
	First    float64                  // seconds before the first chirp
	Level    float64                  // peak amplitude, 0..1
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
	dev, err := openEndpoint(win.ERender, cfg.Endpoint)
	if err != nil {
		return err
	}
	defer dev.Release()
	ac, err := win.NewAudioClient(dev, 300000) // 30 ms
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
	var written int64 // frames written so far
	pending := []int64{}
	nextChirp, nextN := first, 0

	sample := func(i int64) float64 {
		if i < first {
			return 0
		}
		off := (i - first) % period
		if off < int64(len(chirp)) {
			return cfg.Level * chirp[off]
		}
		return 0
	}
	fill := func(n int) error {
		buf, err := win.RenderBuffer(rc, n, f.BlockAlign)
		if err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			v := sample(written + int64(i))
			for c := 0; c < f.Channels; c++ {
				putSample(buf[i*f.BlockAlign+c*f.Bits/8:], f, v)
			}
		}
		for nextChirp < written+int64(n) {
			pending = append(pending, nextChirp)
			nextChirp += period
		}
		written += int64(n)
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

func getSample(b []byte, f win.WaveFormat) float64 {
	switch {
	case f.Float:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
	case f.Bits == 16:
		return float64(int16(binary.LittleEndian.Uint16(b))) / 32768
	case f.Bits == 24:
		return float64(int32(uint32(b[0])<<8|uint32(b[1])<<16|uint32(b[2])<<24)>>8) / 8388608
	default:
		return float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648
	}
}

// Packet is a run of captured samples.
type Packet struct {
	Start  int64 // index of its first sample in the stream
	Frames int
	// DeviceQPC is when its first sample was captured, per the driver (QPC
	// ticks; 0 if unknown).
	DeviceQPC int64
	// Arrive is when caplat got it (QPC ticks).
	Arrive int64
}

// OnSamples receives mono samples (the first channel) and their packet.
type OnSamples func(x []float64, p Packet)

// WASAPICapture reads an input endpoint in shared mode, event-driven.
type WASAPICapture struct {
	worker
	rate int
}

// Rate is the capture's sample rate.
func (c *WASAPICapture) Rate() int { return c.rate }

// StartWASAPI starts capturing; onSamples runs on the capture thread.
func StartWASAPI(endpoint string, now func() int64, freq int64, onSamples OnSamples) (*WASAPICapture, error) {
	c := &WASAPICapture{worker: newWorker()}
	err := c.start(func(ready chan<- error) error { return c.run(endpoint, now, freq, onSamples, ready) })
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (c *WASAPICapture) run(endpoint string, now func() int64, freq int64, onSamples OnSamples, ready chan<- error) error {
	dev, err := openEndpoint(win.ECapture, endpoint)
	if err != nil {
		return err
	}
	defer dev.Release()
	ac, err := win.NewAudioClient(dev, 1000000) // 100 ms of room; packets come every period
	if err != nil {
		return fmt.Errorf("%s: %w", endpoint, err)
	}
	defer ac.Close()
	cc, err := ac.Service(&win.IID_IAudioCaptureClient)
	if err != nil {
		return err
	}
	defer cc.Release()
	f := ac.Format
	c.rate = f.Rate
	if err := ac.Start(); err != nil {
		return err
	}
	defer ac.Stop()
	ready <- nil

	var idx int64
	var mono []float64
	for !c.stopped() {
		ev, _ := windows.WaitForSingleObject(ac.Event, 200)
		if ev != windows.WAIT_OBJECT_0 {
			return errors.New("the audio input stopped signalling (no signal or device removed?)")
		}
		for {
			n, err := win.NextPacketSize(cc)
			if err != nil {
				return err
			}
			if n == 0 {
				break
			}
			pk, err := win.CaptureGet(cc, f.BlockAlign)
			arrive := now()
			if err != nil {
				return err
			}
			mono = mono[:0]
			for i := 0; i < pk.Frames; i++ {
				v := 0.0
				if pk.Flags&win.BufferFlagsSilent == 0 && pk.Data != nil {
					v = getSample(pk.Data[i*f.BlockAlign:], f)
				}
				mono = append(mono, v)
			}
			p := Packet{Start: idx, Frames: pk.Frames, Arrive: arrive}
			if pk.Flags&win.BufferFlagsTimestampErr == 0 && pk.QPC != 0 {
				p.DeviceQPC = hnsToQPC(pk.QPC, freq)
			}
			win.CaptureRelease(cc, pk.Frames)
			idx += int64(pk.Frames)
			onSamples(mono, p)
		}
	}
	return nil
}

// hnsToQPC converts 100 ns units of QPC time to QPC ticks.
func hnsToQPC(hns, freq int64) int64 {
	if freq == 10_000_000 {
		return hns
	}
	return hns/10_000_000*freq + hns%10_000_000*freq/10_000_000
}
