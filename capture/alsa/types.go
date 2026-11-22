package alsa

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrUnsupported is returned on non-Linux operating systems.
var ErrUnsupported = errors.New("alsa: only supported on Linux")

// ALSA PCM constants from <alsa/pcm.h>.
const (
	StreamPlayback int32 = 0
	StreamCapture  int32 = 1

	AccessRWInterleaved int32 = 3

	FormatS16LE   int32 = 2
	FormatS24LE   int32 = 6
	FormatS32LE   int32 = 10
	FormatFloatLE int32 = 14
	FormatS24_3LE int32 = 32

	TstampNone   int32 = 0
	TstampEnable int32 = 1

	TstampTypeGettimeofday int32 = 0
	TstampTypeMonotonic    int32 = 1
	TstampTypeMonotonicRaw int32 = 2

	ErrAgain int32 = -11
	ErrBusy  int32 = -16
	ErrPipe  int32 = -32
)

// Timespec mirrors struct timespec (snd_htimestamp_t) on Linux LP64 (amd64/arm64).
type Timespec struct {
	Sec  int64
	Nsec int64
}

func (t Timespec) Nano() int64 {
	if t.Sec <= 0 && t.Nsec <= 0 {
		return 0
	}
	return t.Sec*1_000_000_000 + t.Nsec
}

// DeviceInfo describes one ALSA hardware PCM endpoint from /proc/asound.
type DeviceInfo struct {
	Card        int
	Device      int
	CardName    string
	DeviceName  string
	MonitorName string // HDMI/DP ELD monitor_name when available
	PCMName     string // e.g. "hw:2,0"
	Playback    bool
	Capture     bool
}

// DisplayName formats the device for CLI/GUI selection lists.
func (d DeviceInfo) DisplayName() string {
	var b strings.Builder
	if d.CardName != "" {
		b.WriteString(d.CardName)
	}
	if d.DeviceName != "" {
		if b.Len() > 0 {
			b.WriteString(" - ")
		}
		b.WriteString(d.DeviceName)
	}
	if d.MonitorName != "" {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('[')
		b.WriteString(d.MonitorName)
		b.WriteByte(']')
	}
	if d.PCMName != "" {
		if b.Len() > 0 {
			b.WriteString(" (")
			b.WriteString(d.PCMName)
			b.WriteByte(')')
		} else {
			b.WriteString(d.PCMName)
		}
	}
	return b.String()
}

// Config describes an ALSA capture stream.
type Config struct {
	Device string
	Rate   int           // desired sample rate (0 -> 48000)
	Channels int         // desired channels (0 -> 2)
	Period time.Duration // desired period duration (0 -> 10 ms)
	// FallbackDefault allows opening "pipewire" or "default" when hw:X,Y
	// fails with EBUSY; SoftwareMixer is set on the returned Capture.
	FallbackDefault bool
}

// Packet is one period of captured audio.
type Packet struct {
	Samples       []float32 // interleaved float32 samples; valid only during callback
	Frames        int
	Start         int64
	Discontinuity bool
	DeviceQPC     int64 // CLOCK_MONOTONIC ns (or ticks scaled to freq); 0 if unknown
	Arrive        int64
}

// Capture is a running ALSA capture.
type Capture struct {
	Rate          int
	Channels      int
	Period        time.Duration
	ActualDevice  string
	SoftwareMixer bool

	done     chan error
	stop     chan struct{}
	stopOnce sync.Once
}

// Done delivers why the capture ended (once).
func (c *Capture) Done() <-chan error { return c.done }

// Stop ends the capture.
func (c *Capture) Stop() { c.stopOnce.Do(func() { close(c.stop) }) }

func (c *Capture) stopped() bool {
	select {
	case <-c.stop:
		return true
	default:
		return false
	}
}

// PlaybackConfig describes an ALSA playback stream.
type PlaybackConfig struct {
	Device          string
	Rate            int           // desired sample rate (0 -> 48000)
	Channels        int           // desired channels (0 -> 2)
	Period          time.Duration // desired period (0 -> 10 ms)
	FallbackDefault bool
}

// Playback is an open ALSA PCM render handle.
type Playback struct {
	Rate          int
	Channels      int
	Period        time.Duration
	Buffer        time.Duration
	PeriodFrames  int
	BufferFrames  int
	ActualDevice  string
	SoftwareMixer bool

	drv    pcmDriver
	h      pcmHandle
	format int32
	scratch []byte
}

// WriteFrames writes interleaved float32 frames to the PCM device, recovering
// from EPIPE underruns once if needed.
func (p *Playback) WriteFrames(samples []float32) (int, error) {
	if len(samples) == 0 || p.Channels <= 0 {
		return 0, nil
	}
	frames := len(samples) / p.Channels
	if frames == 0 {
		return 0, nil
	}
	bps := formatSampleBytes(p.format)
	need := frames * p.Channels * bps
	if cap(p.scratch) < need {
		p.scratch = make([]byte, need)
	}
	p.scratch = p.scratch[:need]
	encodeFromFloat32(p.scratch, samples[:frames*p.Channels], p.format)

	n, err := p.drv.Writei(p.h, p.scratch, uint64(frames))
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// Wait blocks up to timeoutMS for playback buffer space to become available.
func (p *Playback) Wait(timeoutMS int) (bool, error) {
	return p.drv.Wait(p.h, timeoutMS)
}

// Status returns the ALSA monotonic htstamp (nanoseconds, 0 if unavailable) and
// current playback delay in frames.
func (p *Playback) Status() (htstampNS int64, delayFrames int64, err error) {
	ts, _, delay, err := p.drv.Status(p.h)
	if err != nil {
		return 0, 0, err
	}
	return ts.Nano(), delay, nil
}

// Close closes the ALSA playback handle.
func (p *Playback) Close() error {
	if p.h == nil {
		return nil
	}
	h := p.h
	p.h = nil
	return p.drv.Close(h)
}

// FindName returns the one name equal to want, or else the one containing it
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
		return "", fmt.Errorf("no ALSA device matches %q; have %q", want, names)
	}
	return "", fmt.Errorf("%q matches several ALSA devices: %q", want, hits)
}

// PCMName extracts the ALSA PCM identifier ("hw:X,Y", "plughw:X,Y", "default",
// etc.) from a DisplayName like "Live Gamer ULTRA - USB Audio (hw:2,0)".
func PCMName(device string) string {
	s := strings.TrimSpace(device)
	if i := strings.LastIndexByte(s, '('); i >= 0 {
		if j := strings.LastIndexByte(s, ')'); j > i+1 {
			inner := strings.TrimSpace(s[i+1 : j])
			if IsDirectPCM(inner) {
				return inner
			}
		}
	}
	return s
}

// IsDirectPCM reports whether name is a direct ALSA PCM name ("default",
// "pipewire", "pulse", "dmix...", "hw:...", "plughw:...", or "sysdefault:...").
func IsDirectPCM(name string) bool {
	low := strings.ToLower(strings.TrimSpace(name))
	return IsSoftwarePCM(low) || strings.HasPrefix(low, "hw:") ||
		strings.HasPrefix(low, "plughw:") || strings.HasPrefix(low, "sysdefault:")
}

// IsSoftwarePCM reports whether pcmName routes through a software mixer/server.
func IsSoftwarePCM(pcmName string) bool {
	low := strings.ToLower(strings.TrimSpace(pcmName))
	return low == "default" || low == "pipewire" || low == "pulse" || strings.HasPrefix(low, "dmix")
}

var (
	reCardHeader = regexp.MustCompile(`^\s*(\d+)\s+\[[^\]]+\]:\s*(.+)$`)
	rePCMLine    = regexp.MustCompile(`^(\d+)-(\d+):\s*(.+)$`)
)

// ParseProcAsound parses /proc/asound/cards, /proc/asound/pcm, and optional
// per-card HDMI monitor names from /proc/asound/cardN/eld#* into DeviceInfos.
func ParseProcAsound(cardsText, pcmText string, eldByCard map[int][]string) []DeviceInfo {
	cardNames := make(map[int]string)
	sc := bufio.NewScanner(strings.NewReader(cardsText))
	for sc.Scan() {
		m := reCardHeader.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		card, _ := strconv.Atoi(m[1])
		desc := strings.TrimSpace(m[2])
		// Format is typically "Driver - Card Name"; keep the right side when present.
		if _, right, ok := strings.Cut(desc, " - "); ok {
			desc = strings.TrimSpace(right)
		}
		cardNames[card] = desc
	}

	var out []DeviceInfo
	hdmiOrdinal := make(map[int]int)
	sc = bufio.NewScanner(strings.NewReader(pcmText))
	for sc.Scan() {
		m := rePCMLine.FindStringSubmatch(strings.TrimSpace(sc.Text()))
		if m == nil {
			continue
		}
		card, _ := strconv.Atoi(m[1])
		dev, _ := strconv.Atoi(m[2])
		parts := strings.Split(m[3], ":")
		if len(parts) < 3 {
			continue
		}
		devName := strings.TrimSpace(parts[1])
		if devName == "" {
			devName = strings.TrimSpace(parts[0])
		}
		var playback, capture bool
		for _, p := range parts[2:] {
			low := strings.ToLower(strings.TrimSpace(p))
			if strings.HasPrefix(low, "playback") {
				playback = true
			}
			if strings.HasPrefix(low, "capture") {
				capture = true
			}
		}
		mon := ""
		if strings.Contains(strings.ToUpper(devName), "HDMI") || strings.Contains(strings.ToUpper(devName), "DP") {
			idx := hdmiOrdinal[card]
			hdmiOrdinal[card] = idx + 1
			if elds := eldByCard[card]; idx < len(elds) {
				mon = elds[idx]
			}
		}
		out = append(out, DeviceInfo{
			Card:        card,
			Device:      dev,
			CardName:    cardNames[card],
			DeviceName:  devName,
			MonitorName: mon,
			PCMName:     fmt.Sprintf("hw:%d,%d", card, dev),
			Playback:    playback,
			Capture:     capture,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Card != out[j].Card {
			return out[i].Card < out[j].Card
		}
		return out[i].Device < out[j].Device
	})
	return out
}

func formatSampleBytes(format int32) int {
	switch format {
	case FormatS16LE:
		return 2
	case FormatS24_3LE:
		return 3
	case FormatS24LE, FormatS32LE, FormatFloatLE:
		return 4
	default:
		return 0
	}
}

func decodeToFloat32(dst []float32, raw []byte, frames, channels int, format int32) []float32 {
	total := frames * channels
	dst = dst[:0]
	switch format {
	case FormatFloatLE:
		for i := 0; i < total; i++ {
			dst = append(dst, math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:])))
		}
	case FormatS32LE:
		for i := 0; i < total; i++ {
			v := int32(binary.LittleEndian.Uint32(raw[i*4:]))
			dst = append(dst, float32(v)/2147483648.0)
		}
	case FormatS24LE:
		for i := 0; i < total; i++ {
			// S24_LE is 24-bit signed in the low 3 bytes of a 32-bit word.
			u := binary.LittleEndian.Uint32(raw[i*4:])
			v := int32(u<<8) >> 8
			dst = append(dst, float32(v)/8388608.0)
		}
	case FormatS24_3LE:
		for i := 0; i < total; i++ {
			b := raw[i*3:]
			v := int32(uint32(b[0])<<8|uint32(b[1])<<16|uint32(b[2])<<24) >> 8
			dst = append(dst, float32(v)/8388608.0)
		}
	case FormatS16LE:
		for i := 0; i < total; i++ {
			v := int16(binary.LittleEndian.Uint16(raw[i*2:]))
			dst = append(dst, float32(v)/32768.0)
		}
	}
	return dst
}

func encodeFromFloat32(dst []byte, src []float32, format int32) {
	clamp := func(v float32) float64 {
		if v > 1 {
			return 1
		}
		if v < -1 {
			return -1
		}
		return float64(v)
	}
	switch format {
	case FormatFloatLE:
		for i, v := range src {
			binary.LittleEndian.PutUint32(dst[i*4:], math.Float32bits(v))
		}
	case FormatS32LE:
		for i, v := range src {
			binary.LittleEndian.PutUint32(dst[i*4:], uint32(int32(clamp(v)*2147483647.0)))
		}
	case FormatS24LE:
		for i, v := range src {
			s := int32(clamp(v) * 8388607.0)
			binary.LittleEndian.PutUint32(dst[i*4:], uint32(s)&0x00ffffff)
		}
	case FormatS24_3LE:
		for i, v := range src {
			s := int32(clamp(v) * 8388607.0)
			dst[i*3] = byte(s)
			dst[i*3+1] = byte(s >> 8)
			dst[i*3+2] = byte(s >> 16)
		}
	case FormatS16LE:
		for i, v := range src {
			binary.LittleEndian.PutUint16(dst[i*2:], uint16(int16(clamp(v)*32767.0)))
		}
	}
}

func nsToTicks(ns, freq int64) int64 {
	if freq == 1_000_000_000 {
		return ns
	}
	return ns/1_000_000_000*freq + ns%1_000_000_000*freq/1_000_000_000
}

func captureDeviceTicks(htstampNS int64, avail uint64, rate int, arrive, freq int64) int64 {
	if htstampNS <= 0 || rate <= 0 {
		return 0
	}
	devNS := htstampNS - int64(avail)*1_000_000_000/int64(rate)
	if devNS <= 0 {
		return 0
	}
	ticks := nsToTicks(devNS, freq)
	if ticks <= 0 || ticks > arrive {
		return 0
	}
	return ticks
}

// PlaybackChirpTicks computes when chirpFrame leaves the DAC/HDMI transmitter
// using ALSA status htstamp (CLOCK_MONOTONIC ns) and delay (frames), falling
// back to nowTicks when htstampNS <= 0 (e.g. software plugins without htstamp).
func PlaybackChirpTicks(htstampNS, nowTicks, delayFrames, writtenFrames, chirpFrame int64, rate int, freq int64) int64 {
	played := writtenFrames - delayFrames
	ref := nowTicks
	if htstampNS > 0 {
		ref = nsToTicks(htstampNS, freq)
	}
	return ref + (chirpFrame-played)*freq/int64(rate)
}

type hwConfig struct {
	Rate     int
	Channels int
	PeriodUS uint32
	BufferUS uint32
}

type negotiatedHW struct {
	Format       int32
	Rate         int
	Channels     int
	PeriodFrames int
	BufferFrames int
}

type pcmHandle interface{}

type pcmDriver interface {
	Open(name string, stream int32, mode int32) (pcmHandle, int32, error)
	Close(h pcmHandle) error
	Configure(h pcmHandle, cfg hwConfig, isPlayback bool) (negotiatedHW, error)
	Prepare(h pcmHandle) error
	Start(h pcmHandle) error
	Wait(h pcmHandle, timeoutMS int) (ready bool, err error)
	Readi(h pcmHandle, buf []byte, frames uint64) (int64, int32, error)
	Writei(h pcmHandle, buf []byte, frames uint64) (int64, error)
	Recover(h pcmHandle, errCode int32) error
	Status(h pcmHandle) (htstamp Timespec, avail uint64, delay int64, err error)
}

func openPCMWithFallback(drv pcmDriver, device string, stream int32, fallback bool) (pcmHandle, string, bool, error) {
	pcm := PCMName(device)
	h, code, err := drv.Open(pcm, stream, 0)
	if err == nil {
		return h, pcm, IsSoftwarePCM(pcm), nil
	}
	if code != ErrBusy {
		return nil, "", false, fmt.Errorf("ALSA open %q: %w", pcm, err)
	}
	if !fallback {
		return nil, "", false, fmt.Errorf("ALSA device %s is busy (EBUSY, likely held by PipeWire/PulseAudio); retry without --exclusive to allow software-mixer fallback, or disable this card's profile in pavucontrol/wpctl to measure raw hw: latency", pcm)
	}
	for _, fb := range []string{"pipewire", "default"} {
		if fb == pcm {
			continue
		}
		h2, _, err2 := drv.Open(fb, stream, 0)
		if err2 == nil {
			return h2, fb, true, nil
		}
	}
	return nil, "", false, fmt.Errorf("ALSA open %q: %w (pipewire/default fallbacks also failed)", pcm, err)
}

func openPlaybackWith(drv pcmDriver, cfg PlaybackConfig) (*Playback, error) {
	if cfg.Rate <= 0 {
		cfg.Rate = 48000
	}
	if cfg.Channels <= 0 {
		cfg.Channels = 2
	}
	if cfg.Period <= 0 {
		cfg.Period = 10 * time.Millisecond
	}
	h, actual, swMixer, err := openPCMWithFallback(drv, cfg.Device, StreamPlayback, cfg.FallbackDefault)
	if err != nil {
		return nil, err
	}
	periodUS := uint32(max(int64(cfg.Period/time.Microsecond), 1000))
	bufferUS := max(periodUS*3, 30000)
	hw, err := drv.Configure(h, hwConfig{
		Rate: cfg.Rate, Channels: cfg.Channels, PeriodUS: periodUS, BufferUS: bufferUS,
	}, true)
	if err != nil {
		_ = drv.Close(h)
		return nil, err
	}
	if err := drv.Prepare(h); err != nil {
		_ = drv.Close(h)
		return nil, err
	}
	return &Playback{
		Rate:          hw.Rate,
		Channels:      hw.Channels,
		Period:        time.Duration(hw.PeriodFrames) * time.Second / time.Duration(hw.Rate),
		Buffer:        time.Duration(hw.BufferFrames) * time.Second / time.Duration(hw.Rate),
		PeriodFrames:  hw.PeriodFrames,
		BufferFrames:  hw.BufferFrames,
		ActualDevice:  actual,
		SoftwareMixer: swMixer,
		drv:           drv,
		h:             h,
		format:        hw.Format,
	}, nil
}

func startCaptureWith(drv pcmDriver, cfg Config, now func() int64, freq int64, onPacket func(Packet)) (*Capture, error) {
	if cfg.Rate <= 0 {
		cfg.Rate = 48000
	}
	if cfg.Channels <= 0 {
		cfg.Channels = 2
	}
	if cfg.Period <= 0 {
		cfg.Period = 10 * time.Millisecond
	}
	c := &Capture{done: make(chan error, 1), stop: make(chan struct{})}
	ready := make(chan error, 1)

	go func() {
		err := c.run(drv, cfg, now, freq, onPacket, ready)
		select {
		case ready <- err:
		default:
		}
		c.done <- err
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Capture) run(drv pcmDriver, cfg Config, now func() int64, freq int64, onPacket func(Packet), ready chan<- error) error {
	h, actual, swMixer, err := openPCMWithFallback(drv, cfg.Device, StreamCapture, cfg.FallbackDefault)
	if err != nil {
		return err
	}
	defer drv.Close(h)

	periodUS := uint32(max(int64(cfg.Period/time.Microsecond), 1000))
	bufferUS := max(periodUS*4, 40000)
	hw, err := drv.Configure(h, hwConfig{
		Rate: cfg.Rate, Channels: cfg.Channels, PeriodUS: periodUS, BufferUS: bufferUS,
	}, false)
	if err != nil {
		return err
	}
	if err := drv.Prepare(h); err != nil {
		return err
	}
	if err := drv.Start(h); err != nil {
		return err
	}

	c.Rate = hw.Rate
	c.Channels = hw.Channels
	c.ActualDevice = actual
	c.SoftwareMixer = swMixer
	if hw.PeriodFrames > 0 && hw.Rate > 0 {
		c.Period = time.Duration(hw.PeriodFrames) * time.Second / time.Duration(hw.Rate)
	}
	ready <- nil

	periodFrames := max(hw.PeriodFrames, 128)
	bps := formatSampleBytes(hw.Format)
	raw := make([]byte, periodFrames*hw.Channels*bps)
	var f32 []float32
	var idx int64
	var discNext bool

	for !c.stopped() {
		readyWait, err := drv.Wait(h, 200)
		if err != nil {
			return err
		}
		if c.stopped() {
			break
		}
		if !readyWait {
			continue
		}

		ts, avail, _, stErr := drv.Status(h)
		n, code, err := drv.Readi(h, raw, uint64(periodFrames))
		arrive := now()
		if err != nil {
			if code == ErrAgain {
				continue
			}
			if code == ErrPipe {
				if rerr := drv.Recover(h, code); rerr != nil {
					return rerr
				}
				discNext = true
				continue
			}
			return err
		}
		if n <= 0 {
			continue
		}
		frames := int(n)
		var devTicks int64
		if stErr == nil && !swMixer {
			devTicks = captureDeviceTicks(ts.Nano(), avail, hw.Rate, arrive, freq)
		}
		f32 = decodeToFloat32(f32, raw, frames, hw.Channels, hw.Format)
		p := Packet{
			Samples:       f32,
			Frames:        frames,
			Start:         idx,
			Discontinuity: discNext,
			DeviceQPC:     devTicks,
			Arrive:        arrive,
		}
		discNext = false
		idx += int64(frames)
		onPacket(p)
	}
	return nil
}
