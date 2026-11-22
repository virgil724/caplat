package alsa

import (
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/virgil724/caplat/displays"
)

func TestALSALayoutsAndConstants(t *testing.T) {
	if got := unsafe.Sizeof(Timespec{}); got != 16 {
		t.Errorf("Timespec size = %d, want 16", got)
	}
	if got := unsafe.Offsetof(Timespec{}.Sec); got != 0 {
		t.Errorf("Timespec.Sec offset = %d, want 0", got)
	}
	if got := unsafe.Offsetof(Timespec{}.Nsec); got != 8 {
		t.Errorf("Timespec.Nsec offset = %d, want 8", got)
	}
	if StreamPlayback != 0 || StreamCapture != 1 || AccessRWInterleaved != 3 {
		t.Errorf("unexpected stream/access constants")
	}
	if FormatS16LE != 2 || FormatS24LE != 6 || FormatS32LE != 10 || FormatFloatLE != 14 || FormatS24_3LE != 32 {
		t.Errorf("unexpected format constants")
	}
	if TstampEnable != 1 || TstampTypeMonotonic != 1 {
		t.Errorf("unexpected tstamp constants")
	}
}

func TestParseProcAsoundAndPickDevice(t *testing.T) {
	const cards = ` 0 [NVidia         ]: HDA-Intel - HDA NVidia
                      HDA NVidia at 0xf6080000 irq 87
 2 [ULTRA          ]: USB-Audio - Live Gamer ULTRA
                      AVerMedia Technologies, Inc. Live Gamer ULTRA at usb-0000:00:14.0
 3 [Loopback       ]: Loopback - Loopback
                      Loopback 1
`
	const pcm = `00-03: HDMI 0 : HDMI 0 : playback 1
00-07: HDMI 1 : HDMI 1 : playback 1
02-00: USB Audio : USB Audio : capture 1
03-00: Loopback PCM : Loopback PCM : playback 8 : capture 8
`
	elds := map[int][]string{
		0: {"AVT GC553", ""},
	}
	devs := ParseProcAsound(cards, pcm, elds)
	if len(devs) != 4 {
		t.Fatalf("got %d devices: %+v", len(devs), devs)
	}
	var outs, ins []string
	for _, d := range devs {
		if d.Playback {
			outs = append(outs, d.DisplayName())
		}
		if d.Capture {
			ins = append(ins, d.DisplayName())
		}
	}
	if got := displays.PickDevice(outs); got != "HDA NVidia - HDMI 0 [AVT GC553] (hw:0,3)" {
		t.Errorf("PickDevice(outs) = %q", got)
	}
	if got := displays.PickDevice(ins); got != "Live Gamer ULTRA - USB Audio (hw:2,0)" {
		t.Errorf("PickDevice(ins) = %q", got)
	}
	if got := PCMName("HDA NVidia - HDMI 0 [AVT GC553] (hw:0,3)"); got != "hw:0,3" {
		t.Errorf("PCMName = %q", got)
	}
	if got := PCMName("pipewire"); got != "pipewire" || !IsSoftwarePCM(got) {
		t.Errorf("PCMName(pipewire) = %q", got)
	}
}

func TestSampleEncodeDecodeRoundTrip(t *testing.T) {
	in := []float32{0, 0.5, -0.5, 0.25, -0.75}
	for _, fmtID := range []int32{FormatFloatLE, FormatS32LE, FormatS24LE, FormatS24_3LE, FormatS16LE} {
		raw := make([]byte, len(in)*formatSampleBytes(fmtID))
		encodeFromFloat32(raw, in, fmtID)
		got := decodeToFloat32(nil, raw, len(in), 1, fmtID)
		if len(got) != len(in) {
			t.Fatalf("fmt %d len = %d, want %d", fmtID, len(got), len(in))
		}
		for i := range in {
			if math.Abs(float64(got[i]-in[i])) > 1e-4 {
				t.Errorf("fmt %d sample %d: got %f want %f", fmtID, i, got[i], in[i])
			}
		}
	}
}

func TestTimestampMath(t *testing.T) {
	const freq = 1_000_000_000
	arrive := int64(20_000_000_000)
	// htstamp = 19.995s, avail = 480 frames @ 48kHz = 10ms -> dev = 19.985s
	htNS := int64(19_995_000_000)
	dev := captureDeviceTicks(htNS, 480, 48000, arrive, freq)
	if dev != 19_985_000_000 {
		t.Errorf("captureDeviceTicks = %d, want 19985000000", dev)
	}
	// Future htstamp (> arrive) must clamp to 0.
	if bad := captureDeviceTicks(21_000_000_000, 0, 48000, arrive, freq); bad != 0 {
		t.Errorf("future captureDeviceTicks = %d, want 0", bad)
	}

	// Playback: written=2400, delay=960 (played=1440), chirp at frame 1920 -> +10ms from htstamp.
	at := PlaybackChirpTicks(10_000_000_000, 10_001_000_000, 960, 2400, 1920, 48000, freq)
	if at != 10_010_000_000 {
		t.Errorf("PlaybackChirpTicks = %d, want 10010000000", at)
	}
	// Software plugin fallback with zero htstamp uses nowTicks.
	atSW := PlaybackChirpTicks(0, 10_001_000_000, 960, 2400, 1920, 48000, freq)
	if atSW != 10_011_000_000 {
		t.Errorf("PlaybackChirpTicks fallback = %d, want 10011000000", atSW)
	}
}

type fakePCMDriver struct {
	mu          sync.Mutex
	busyNames   map[string]bool
	openedNames []string
	injectEPIPE bool
	epipeSent   bool
	pktCount    atomic.Int32
}

func (d *fakePCMDriver) Open(name string, stream int32, mode int32) (pcmHandle, int32, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.openedNames = append(d.openedNames, name)
	if d.busyNames[name] {
		return nil, ErrBusy, errors.New("device or resource busy")
	}
	return &name, 0, nil
}

func (d *fakePCMDriver) Close(pcmHandle) error { return nil }

func (d *fakePCMDriver) Configure(_ pcmHandle, cfg hwConfig, _ bool) (negotiatedHW, error) {
	return negotiatedHW{
		Format: FormatS16LE, Rate: cfg.Rate, Channels: cfg.Channels,
		PeriodFrames: 480, BufferFrames: 1920,
	}, nil
}

func (d *fakePCMDriver) Prepare(pcmHandle) error { return nil }
func (d *fakePCMDriver) Start(pcmHandle) error   { return nil }
func (d *fakePCMDriver) Wait(pcmHandle, int) (bool, error) {
	time.Sleep(time.Millisecond)
	return true, nil
}

func (d *fakePCMDriver) Readi(_ pcmHandle, buf []byte, frames uint64) (int64, int32, error) {
	d.mu.Lock()
	if d.injectEPIPE && !d.epipeSent {
		d.epipeSent = true
		d.mu.Unlock()
		return 0, ErrPipe, errors.New("broken pipe")
	}
	d.mu.Unlock()
	samples := make([]float32, int(frames)*2)
	for i := range samples {
		samples[i] = 0.25
	}
	encodeFromFloat32(buf[:int(frames)*4], samples, FormatS16LE)
	return int64(frames), 0, nil
}

func (d *fakePCMDriver) Writei(_ pcmHandle, buf []byte, frames uint64) (int64, error) {
	return int64(frames), nil
}

func (d *fakePCMDriver) Recover(pcmHandle, int32) error { return nil }

func (d *fakePCMDriver) Status(pcmHandle) (Timespec, uint64, int64, error) {
	return Timespec{Sec: 50, Nsec: 20_000_000}, 480, 960, nil
}

func TestFakeDriverCaptureEPIPEAndBusyFallback(t *testing.T) {
	drv := &fakePCMDriver{
		busyNames:   map[string]bool{"hw:2,0": true, "pipewire": true},
		injectEPIPE: true,
	}

	// Exclusive mode (FallbackDefault=false) should return an actionable EBUSY error.
	_, err := startCaptureWith(drv, Config{
		Device:          "Live Gamer ULTRA (hw:2,0)",
		FallbackDefault: false,
	}, func() int64 { return 50_050_000_000 }, 1_000_000_000, func(Packet) {})
	if err == nil || !strings.Contains(err.Error(), "PipeWire") {
		t.Fatalf("expected PipeWire EBUSY diagnostic, got %v", err)
	}

	// FallbackDefault=true should fall through pipewire -> default and mark SoftwareMixer=true.
	gotCh := make(chan Packet, 4)
	c, err := startCaptureWith(drv, Config{
		Device:          "Live Gamer ULTRA (hw:2,0)",
		FallbackDefault: true,
	}, func() int64 { return 50_050_000_000 }, 1_000_000_000, func(p Packet) {
		if drv.pktCount.Add(1) <= 2 {
			gotCh <- p
		}
	})
	if err != nil {
		t.Fatalf("startCaptureWith fallback: %v", err)
	}
	p1 := <-gotCh
	c.Stop()
	<-c.Done()

	if !c.SoftwareMixer || c.ActualDevice != "default" {
		t.Errorf("SoftwareMixer=%v ActualDevice=%q, want (true, default)", c.SoftwareMixer, c.ActualDevice)
	}
	if !p1.Discontinuity {
		t.Errorf("expected Discontinuity=true after EPIPE recovery")
	}
	if p1.DeviceQPC != 0 {
		t.Errorf("software mixer should suppress hw DeviceQPC, got %d", p1.DeviceQPC)
	}
}
