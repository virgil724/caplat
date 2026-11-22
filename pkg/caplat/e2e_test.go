package caplat

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/virgil724/caplat/capture"
	"github.com/virgil724/caplat/internal/audio"
	"github.com/virgil724/caplat/internal/barcode"
	"github.com/virgil724/caplat/displays"
	"github.com/virgil724/caplat/internal/present"
)

func assertClose(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > tol {
		t.Errorf("%s = %.4f, want %.4f (tol %.4f)", name, got, want, tol)
	}
}

type mockCaptureSource struct {
	done     chan error
	stopOnce sync.Once
	onStop   func()
}

func newMockCaptureSource(onStop func()) *mockCaptureSource {
	return &mockCaptureSource{done: make(chan error, 1), onStop: onStop}
}

func (s *mockCaptureSource) Done() <-chan error { return s.done }

func (s *mockCaptureSource) Stop() {
	s.stopOnce.Do(func() {
		if s.onStop != nil {
			s.onStop()
		}
		s.done <- nil
	})
}

type mockPresenter struct {
	mu       sync.Mutex
	onFrame  func(capture.Frame)
	onVblank func(uint64, int64)
	capW     int
	capH     int
	outW     int
	outH     int
	baseTick int64
	seqIdx   int
	totMS    []float64
	devMS    []float64
	clock    *atomic.Int64

	band []byte
	luma []byte
}

func (p *mockPresenter) Open(d displays.Display, onVblank func(uint64, int64)) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onVblank = onVblank
	p.outW, p.outH = d.Width, d.Height
	cell := p.outW / barcode.Cells
	p.band = make([]byte, p.outW*cell*4)
	p.luma = make([]byte, p.capW*p.capH)
	return nil
}

func (p *mockPresenter) Present(show uint64) error {
	p.mu.Lock()
	onFrame := p.onFrame
	onVblank := p.onVblank
	p.mu.Unlock()

	cell := p.outW / barcode.Cells
	present.DrawBand(p.band, p.outW, p.outW*4, cell, barcode.Encode(uint32(show&barcode.Mask)))

	// Nearest-neighbor scale the top band from OutW x OutH into CapW x CapH luma.
	capBandH := min(p.capH, cell*p.capH/p.outH+2)
	for y := 0; y < capBandH; y++ {
		sy := y * p.outH / p.capH
		if sy >= cell {
			sy = cell - 1
		}
		for x := 0; x < p.capW; x++ {
			sx := x * p.outW / p.capW
			p.luma[y*p.capW+x] = p.band[(sy*p.outW+sx)*4]
		}
	}

	vblank := p.baseTick + int64(p.seqIdx)*16_666_667
	p.seqIdx++

	var devTick, arrTick int64
	if show == 0 {
		arrTick = vblank + 10_000_000
	} else {
		i := int(show-1) % len(p.totMS)
		totTicks := int64(math.Round(p.totMS[i] * 1e6))
		devTicks := int64(math.Round(p.devMS[i] * 1e6))
		arrTick = vblank + totTicks
		devTick = vblank + devTicks
		if onVblank != nil {
			onVblank(show, vblank)
		}
	}
	p.clock.Store(arrTick)
	if onFrame != nil {
		onFrame(capture.Frame{
			Y:         p.luma,
			Stride:    p.capW,
			Width:     p.capW,
			Height:    p.capH,
			QPC:       arrTick,
			DeviceQPC: devTick,
		})
	}
	return nil
}

func (p *mockPresenter) Close() error { return nil }

type mockVideoBackend struct {
	disps []Display
	devs  []string
	pres  *mockPresenter
}

func (m *mockVideoBackend) Displays() ([]Display, error)                    { return m.disps, nil }
func (m *mockVideoBackend) Devices(context.Context, string) ([]string, error) { return m.devs, nil }
func (m *mockVideoBackend) DevicesMF() ([]string, error)    { return m.devs, nil }
func (m *mockVideoBackend) DevicesDShow() ([]string, error) { return m.devs, nil }
func (m *mockVideoBackend) DevicesV4L2() ([]string, error)  { return m.devs, nil }
func (m *mockVideoBackend) Modes(context.Context, string, string) ([]Mode, error) {
	return nil, nil
}
func (m *mockVideoBackend) ValidateBackend(string) error   { return nil }
func (m *mockVideoBackend) ValidatePresenter(string) error { return nil }
func (m *mockVideoBackend) ListCaptureDevices(context.Context, Config) ([]string, error) {
	return m.devs, nil
}
func (m *mockVideoBackend) PrepareCapture(_ context.Context, cfg Config, d Display, _ string) (Setup, captureStarter, error) {
	return Setup{CaptureMode: fmt.Sprintf("%dx%d@%s nv12 (Mock)", cfg.CaptureWidth, cfg.CaptureHeight, d.FrameRate())}, m, nil
}
func (m *mockVideoBackend) Start(_ context.Context, _ func() int64, _ int64, onFrame func(capture.Frame)) (capture.Source, string, error) {
	m.pres.mu.Lock()
	m.pres.onFrame = onFrame
	m.pres.mu.Unlock()
	return newMockCaptureSource(nil), "", nil
}
func (m *mockVideoBackend) NewPresenter(string) present.Presenter { return m.pres }

func TestSyntheticVideoE2E(t *testing.T) {
	const freq = 1_000_000_000
	var clk atomic.Int64
	clk.Store(1_000_000_000_000)

	totSeq := []float64{10.05, 12.05, 15.05, 18.05, 20.05}
	devSeq := []float64{7.05, 9.05, 11.05, 13.05, 15.05}

	pres := &mockPresenter{
		capW: 1280, capH: 720,
		baseTick: clk.Load(),
		totMS:    totSeq,
		devMS:    devSeq,
		clock:    &clk,
	}
	vb := &mockVideoBackend{
		disps: []Display{
			{Name: "DP-1", FriendlyName: "Main Monitor", Width: 2560, Height: 1440, RefreshNum: 144, RefreshDen: 1, Primary: true},
			{Name: "DP-2", FriendlyName: "AVT GC553", Width: 1920, Height: 1080, RefreshNum: 60, RefreshDen: 1, LikelyCard: true},
		},
		devs: []string{"OBS Virtual Camera", "Live Gamer Ultra-Video"},
		pres: pres,
	}

	var csvBuf bytes.Buffer
	cfg := Config{
		Display:       "auto",
		CaptureWidth:  1280,
		CaptureHeight: 720,
		Warmup:        100 * time.Millisecond, // 6 frames at 60 Hz
		Duration:      time.Second,            // 60 frames; Settle=10 -> 50 counted
		CSV:           &csvBuf,
	}
	res, err := runWith(context.Background(), cfg, nil, runDeps{
		vb:         vb,
		now:        clk.Load,
		freq:       freq,
		drainDelay: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("runWith: %v", err)
	}

	if res.Setup.Display != "DP-2" || res.Setup.Device != "Live Gamer Ultra-Video" {
		t.Fatalf("unexpected setup: %+v", res.Setup)
	}
	if res.Total.N != 50 {
		t.Fatalf("Total.N = %d, want 50", res.Total.N)
	}
	assertClose(t, "Total.Min", res.Total.Min, 10.05, 0.01)
	assertClose(t, "Total.P10", res.Total.P10, 10.05, 0.01)
	assertClose(t, "Total.P50", res.Total.P50, 15.05, 0.01)
	assertClose(t, "Total.P90", res.Total.P90, 20.05, 0.01)
	assertClose(t, "Total.P99", res.Total.P99, 20.05, 0.01)
	assertClose(t, "Total.Max", res.Total.Max, 20.05, 0.01)
	assertClose(t, "Total.Mean", res.Total.Mean, 15.05, 0.01)

	if res.Device.N != 50 {
		t.Fatalf("Device.N = %d, want 50", res.Device.N)
	}
	assertClose(t, "Device.Min", res.Device.Min, 7.05, 0.01)
	assertClose(t, "Device.P50", res.Device.P50, 11.05, 0.01)
	assertClose(t, "Device.Max", res.Device.Max, 15.05, 0.01)
	assertClose(t, "Device.Mean", res.Device.Mean, 11.05, 0.01)

	if res.Counts.Captured != 66 || res.Counts.DecodeFail != 0 || res.Counts.Mismatched != 0 || res.Counts.OnlyVblank != 0 || res.Counts.OnlyCapture != 0 {
		t.Fatalf("unexpected counts: %+v", res.Counts)
	}

	// Verify CSV header and all 50 data rows.
	lines := strings.Split(strings.TrimSpace(csvBuf.String()), "\n")
	if len(lines) != 52 {
		t.Fatalf("CSV lines = %d, want 52", len(lines))
	}
	if !strings.HasPrefix(lines[0], "# caplat display=DP-2") {
		t.Errorf("unexpected CSV comment: %q", lines[0])
	}
	if lines[1] != "frame,vblank_qpc,arrive_qpc,latency_ms,device_qpc,device_ms" {
		t.Errorf("unexpected CSV header: %q", lines[1])
	}
	rows, err := csv.NewReader(strings.NewReader(strings.Join(lines[2:], "\n"))).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	for idx, row := range rows {
		frameNum, _ := strconv.ParseUint(row[0], 10, 64)
		wantFrame := uint64(11 + idx)
		if frameNum != wantFrame {
			t.Fatalf("row %d frame = %d, want %d", idx, frameNum, wantFrame)
		}
		latMS, _ := strconv.ParseFloat(row[3], 64)
		devMS, _ := strconv.ParseFloat(row[5], 64)
		seqIdx := int(frameNum-1) % len(totSeq)
		assertClose(t, fmt.Sprintf("csv[%d].latency_ms", idx), latMS, totSeq[seqIdx], 0.005)
		assertClose(t, fmt.Sprintf("csv[%d].device_ms", idx), devMS, devSeq[seqIdx], 0.005)
	}
}

type mockAudioBackend struct {
	outs         []string
	ins          []string
	rate         int
	baseTick     int64
	delMS        []float64
	devMS        []float64
	chirpCount   int // 0 = loop until stopped
	clock        *atomic.Int64
	onFirstChirp func()
	onAllDone    func()
}

func (m *mockAudioBackend) ValidateBackend(string) error { return nil }
func (m *mockAudioBackend) Outputs() ([]string, error)   { return m.outs, nil }
func (m *mockAudioBackend) Inputs() ([]string, error)    { return m.ins, nil }

type mockAudioCapture struct {
	*mockCaptureSource
	rate      int
	onSamples audio.OnSamples
}

func (c *mockAudioCapture) Rate() int { return c.rate }

type mockAudioPlayer struct {
	done     chan error
	stopOnce sync.Once
	stopCh   chan struct{}
}

func (p *mockAudioPlayer) Done() <-chan error { return p.done }
func (p *mockAudioPlayer) Stop()              { p.stopOnce.Do(func() { close(p.stopCh) }) }

func (m *mockAudioBackend) StartCapture(_ context.Context, _ AudioConfig, _ string, _ func() int64, _ int64, onSamples audio.OnSamples) (audioSource, time.Duration, []string, error) {
	c := &mockAudioCapture{
		mockCaptureSource: newMockCaptureSource(nil),
		rate:              m.rate,
		onSamples:         onSamples,
	}
	return c, 10 * time.Millisecond, nil, nil
}

func (m *mockAudioBackend) StartPlayer(cfg audio.PlayerConfig, _ int64, onEmit func(audio.Emitted)) (audioPlayer, int, error) {
	pl := &mockAudioPlayer{done: make(chan error, 1), stopCh: make(chan struct{})}
	return pl, m.rate, nil
}

// runSyntheticAudioBackend drives deterministic chirp emission + capture packets
// once StartPlayer is called.
type wiredMockAudioBackend struct {
	mockAudioBackend
	cap *mockAudioCapture
}

func (w *wiredMockAudioBackend) StartCapture(ctx context.Context, cfg AudioConfig, input string, now func() int64, freq int64, onSamples audio.OnSamples) (audioSource, time.Duration, []string, error) {
	src, period, args, err := w.mockAudioBackend.StartCapture(ctx, cfg, input, now, freq, onSamples)
	w.cap = src.(*mockAudioCapture)
	return src, period, args, err
}

func (w *wiredMockAudioBackend) StartPlayer(cfg audio.PlayerConfig, _ int64, onEmit func(audio.Emitted)) (audioPlayer, int, error) {
	pl := &mockAudioPlayer{done: make(chan error, 1), stopCh: make(chan struct{})}
	chirp := cfg.Chirp(w.rate)
	silence := make([]float64, len(chirp)*4)
	go func() {
		var sampleIdx int64
		for i := 0; w.chirpCount == 0 || i < w.chirpCount; i++ {
			select {
			case <-pl.stopCh:
				return
			default:
			}
			played := w.baseTick + int64(i)*500_000_000
			k := i % len(w.delMS)
			delTicks := int64(math.Round(w.delMS[k] * 1e6))
			devTicks := int64(math.Round(w.devMS[k] * 1e6))
			arrTick := played + delTicks
			devTick := played + devTicks

			w.clock.Store(arrTick)
			onEmit(audio.Emitted{N: i, QPC: played})

			// Push chirp at start of packet, then trailing silence so Detector emits OnHit.
			w.cap.onSamples(chirp, audio.Packet{
				Start:     sampleIdx,
				Frames:    len(chirp),
				DeviceQPC: devTick,
				Arrive:    arrTick,
			})
			sampleIdx += int64(len(chirp))
			w.cap.onSamples(silence, audio.Packet{
				Start:  sampleIdx,
				Frames: len(silence),
				Arrive: arrTick + 10_000_000,
			})
			sampleIdx += int64(len(silence))

			if i == 0 && w.onFirstChirp != nil {
				w.onFirstChirp()
			}
			if w.chirpCount > 0 && i+1 == w.chirpCount && w.onAllDone != nil {
				w.onAllDone()
			}
			if w.chirpCount == 0 {
				time.Sleep(15 * time.Millisecond)
			}
		}
	}()
	return pl, w.rate, nil
}

func TestSyntheticAudioE2E(t *testing.T) {
	const freq = 1_000_000_000
	var clk atomic.Int64
	clk.Store(2_000_000_000_000)

	delSeq := []float64{20.05, 22.05, 25.05, 28.05, 30.05}
	devSeq := []float64{10.05, 11.05, 12.05, 13.05, 14.05}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ab := &wiredMockAudioBackend{
		mockAudioBackend: mockAudioBackend{
			outs:       []string{"Speakers", "4 - AVT GC553 (HDMI)"},
			ins:        []string{"Microphone", "HDMI (Live Gamer Ultra-Audio)"},
			rate:       48000,
			baseTick:   clk.Load(),
			delMS:      delSeq,
			devMS:      devSeq,
			chirpCount: 5,
			clock:      &clk,
			onAllDone:  cancel,
		},
	}

	var csvBuf bytes.Buffer
	res, err := runAudioWith(ctx, AudioConfig{
		CSV: &csvBuf,
	}, audioDeps{
		ab:         ab,
		now:        clk.Load,
		freq:       freq,
		drainDelay: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("runAudioWith: %v", err)
	}

	if res.Output != "4 - AVT GC553 (HDMI)" || res.Input != "HDMI (Live Gamer Ultra-Audio)" {
		t.Fatalf("picked wrong endpoints: out=%q in=%q", res.Output, res.Input)
	}
	if res.Emitted != 5 || res.Detected != 5 || res.Matched != 5 {
		t.Fatalf("counts emitted=%d detected=%d matched=%d, want 5/5/5", res.Emitted, res.Detected, res.Matched)
	}
	assertClose(t, "Delivered.Min", res.Delivered.Min, 20.05, 0.01)
	assertClose(t, "Delivered.P50", res.Delivered.P50, 25.05, 0.01)
	assertClose(t, "Delivered.Max", res.Delivered.Max, 30.05, 0.01)
	assertClose(t, "Delivered.Mean", res.Delivered.Mean, 25.05, 0.01)

	assertClose(t, "Device.Min", res.Device.Min, 10.05, 0.01)
	assertClose(t, "Device.P50", res.Device.P50, 12.05, 0.01)
	assertClose(t, "Device.Max", res.Device.Max, 14.05, 0.01)
	assertClose(t, "Device.Mean", res.Device.Mean, 12.05, 0.01)

	lines := strings.Split(strings.TrimSpace(csvBuf.String()), "\n")
	if len(lines) != 7 {
		t.Fatalf("audio CSV lines = %d, want 7", len(lines))
	}
	if lines[1] != "chirp,played_qpc,arrive_qpc,delivered_ms,device_qpc,device_ms" {
		t.Errorf("unexpected audio CSV header: %q", lines[1])
	}
	rows, err := csv.NewReader(strings.NewReader(strings.Join(lines[2:], "\n"))).ReadAll()
	if err != nil {
		t.Fatalf("parse audio CSV: %v", err)
	}
	for i, row := range rows {
		delMS, _ := strconv.ParseFloat(row[3], 64)
		devMS, _ := strconv.ParseFloat(row[5], 64)
		assertClose(t, fmt.Sprintf("acsv[%d].delivered_ms", i), delMS, delSeq[i], 0.005)
		assertClose(t, fmt.Sprintf("acsv[%d].device_ms", i), devMS, devSeq[i], 0.005)
	}
}

func TestSyntheticAVE2E(t *testing.T) {
	const freq = 1_000_000_000
	var vclk, aclk atomic.Int64
	vclk.Store(3_000_000_000_000)
	aclk.Store(3_000_000_000_000)

	pres := &mockPresenter{
		capW: 1280, capH: 720,
		baseTick: vclk.Load(),
		totMS:    []float64{15.05},
		devMS:    []float64{8.05},
		clock:    &vclk,
	}
	vb := &mockVideoBackend{
		disps: []Display{{Name: "DP-2", FriendlyName: "AVT GC553", Width: 1920, Height: 1080, RefreshNum: 60, RefreshDen: 1, LikelyCard: true}},
		devs:  []string{"Live Gamer Ultra-Video"},
		pres:  pres,
	}
	firstChirp := make(chan struct{})
	var firstOnce sync.Once
	ab := &wiredMockAudioBackend{
		mockAudioBackend: mockAudioBackend{
			outs:         []string{"AVT GC553 (HDMI)"},
			ins:          []string{"Live Gamer Ultra-Audio"},
			rate:         48000,
			baseTick:     aclk.Load(),
			delMS:        []float64{22.05},
			devMS:        []float64{12.05},
			clock:        &aclk,
			onFirstChirp: func() { firstOnce.Do(func() { close(firstChirp) }) },
		},
	}

	presWait := &waitOnClosePresenter{Presenter: pres, ready: firstChirp}
	vb.pres = nil
	res, err := runAVWith(
		context.Background(),
		Config{Display: "auto", CaptureWidth: 1280, CaptureHeight: 720, Warmup: 50 * time.Millisecond, Duration: 500 * time.Millisecond},
		AudioConfig{},
		nil,
		runDeps{vb: &waitVideoBackend{mockVideoBackend: vb, presWait: presWait}, now: vclk.Load, freq: freq, drainDelay: 10 * time.Millisecond},
		audioDeps{ab: ab, now: aclk.Load, freq: freq, drainDelay: 10 * time.Millisecond},
	)
	if err != nil {
		t.Fatalf("runAVWith: %v", err)
	}
	assertClose(t, "Skew", res.Skew(), 7.00, 0.01)
	assertClose(t, "DriverSkew", res.DriverSkew(), 4.00, 0.01)
}

type waitOnClosePresenter struct {
	present.Presenter
	ready chan struct{}
}

func (w *waitOnClosePresenter) Close() error {
	<-w.ready
	return w.Presenter.Close()
}

type waitVideoBackend struct {
	*mockVideoBackend
	presWait *waitOnClosePresenter
}

func (w *waitVideoBackend) Start(ctx context.Context, now func() int64, freq int64, onFrame func(capture.Frame)) (capture.Source, string, error) {
	p := w.presWait.Presenter.(*mockPresenter)
	p.mu.Lock()
	p.onFrame = onFrame
	p.mu.Unlock()
	return newMockCaptureSource(nil), "", nil
}

func (w *waitVideoBackend) PrepareCapture(_ context.Context, cfg Config, d Display, _ string) (Setup, captureStarter, error) {
	return Setup{CaptureMode: fmt.Sprintf("%dx%d@%s nv12 (Mock)", cfg.CaptureWidth, cfg.CaptureHeight, d.FrameRate())}, w, nil
}

func (w *waitVideoBackend) NewPresenter(string) present.Presenter { return w.presWait }

func TestPickEndpointDirectPCM(t *testing.T) {
	names := []string{"HDA NVidia - HDMI 0 (hw:0,3)"}
	for _, pcm := range []string{"default", "pipewire", "plughw:0,3"} {
		got, err := pickEndpoint(names, pcm, "output", AudioALSA)
		if err != nil || got != pcm {
			t.Errorf("pickEndpoint(%q, AudioALSA) = (%q, %v)", pcm, got, err)
		}
	}
	if _, err := pickEndpoint(names, "default", "output", AudioWASAPI); err == nil {
		t.Errorf("pickEndpoint(default, AudioWASAPI) should reject unknown endpoint")
	}
}
