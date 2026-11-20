//go:build windows

package wasapi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"github.com/virgil724/caplat/internal/win"
)

// Inputs and Outputs list the active capture and render endpoints' names.
func Inputs() ([]string, error)  { return endpoints(win.ECapture) }
func Outputs() ([]string, error) { return endpoints(win.ERender) }

func endpoints(flow int) ([]string, error) {
	var names []string
	err := win.WithCOM(func() error {
		eps, err := win.AudioEndpoints(flow)
		for _, e := range eps {
			names = append(names, e.Name)
			e.Device.Release()
		}
		return err
	})
	return names, err
}

// Config describes a capture.
type Config struct {
	Endpoint string // capture endpoint name, as Inputs lists it
	// Period asks for a shorter engine period than the default (about
	// 10 ms) through IAudioClient3; 0 keeps the default. The driver may
	// round it up or not support it, see Capture.Period.
	Period time.Duration
	// Exclusive opens the endpoint in exclusive mode, bypassing the audio
	// engine, with an event every Period (0 = the device's minimum, often
	// 3 ms). No other program can use the endpoint meanwhile.
	Exclusive bool
}

// Packet is one packet of captured audio.
type Packet struct {
	// Samples holds Frames frames of interleaved float32 samples, all
	// channels; valid only during the callback.
	Samples []float32
	Frames  int
	// Start is the index of the packet's first frame in the stream.
	Start int64
	// Silent is set when the driver flagged the packet as silence (Samples
	// are zeros) and Discontinuity when frames were lost before it.
	Silent, Discontinuity bool
	// DeviceQPC is when its first frame was captured, per the driver, in
	// QPC ticks (0 if unknown); Arrive is when it reached the callback.
	DeviceQPC, Arrive int64
}

// Capture is a running capture.
type Capture struct {
	Rate, Channels int
	// Period is the engine period in use; 0 is the default (about 10 ms).
	Period time.Duration

	done     chan error
	stop     chan struct{}
	stopOnce sync.Once
}

// Done delivers why the capture ended.
func (c *Capture) Done() <-chan error { return c.done }

// Stop ends the capture.
func (c *Capture) Stop() { c.stopOnce.Do(func() { close(c.stop) }) }

// Start opens the endpoint and calls onPacket for every packet, on the
// capture thread. now is the QPC clock and freq its ticks per second.
func Start(cfg Config, now func() int64, freq int64, onPacket func(Packet)) (*Capture, error) {
	c := &Capture{done: make(chan error, 1), stop: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		err := win.WithCOM(func() error { return c.run(cfg, now, freq, onPacket, ready) })
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

func (c *Capture) stopped() bool {
	select {
	case <-c.stop:
		return true
	default:
		return false
	}
}

func (c *Capture) run(cfg Config, now func() int64, freq int64, onPacket func(Packet), ready chan<- error) error {
	dev, err := win.OpenEndpoint(win.ECapture, cfg.Endpoint)
	if err != nil {
		return err
	}
	defer dev.Release()
	// The format's rate is not known before activating; ask in frames at
	// 48 kHz, which the engine rounds to its own fundamental period.
	var ac *win.AudioClient
	if cfg.Exclusive {
		ac, err = win.NewExclusiveClient(dev, int64(cfg.Period/100))
	} else {
		ac, err = win.NewAudioClient(dev, 1000000, int(cfg.Period.Seconds()*48000)) // 100 ms of room
	}
	if err != nil {
		return fmt.Errorf("%s: %w", cfg.Endpoint, err)
	}
	defer ac.Close()
	cc, err := ac.Service(&win.IID_IAudioCaptureClient)
	if err != nil {
		return err
	}
	defer cc.Release()
	f := ac.Format
	c.Rate, c.Channels = f.Rate, f.Channels
	if ac.Period > 0 {
		c.Period = time.Duration(ac.Period) * time.Second / time.Duration(f.Rate)
	}
	if err := ac.Start(); err != nil {
		return err
	}
	defer ac.Stop()
	ready <- nil

	var idx int64
	var buf []float32
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
			p := Packet{Frames: pk.Frames, Start: idx, Arrive: arrive,
				Silent: pk.Flags&win.BufferFlagsSilent != 0, Discontinuity: pk.Flags&win.BufferFlagsDiscontinuity != 0}
			if pk.Flags&win.BufferFlagsTimestampErr == 0 && pk.QPC != 0 {
				p.DeviceQPC = HNSToQPC(pk.QPC, freq)
				if cfg.Exclusive {
					// In exclusive mode the timestamp marks the end of the
					// packet, not its first frame: measured on a GC553, a
					// chirp's packet arrived 0-3 ms (one period) *before*
					// the uncorrected time of its first sample.
					p.DeviceQPC -= int64(pk.Frames) * freq / int64(f.Rate)
				}
			}
			buf = convert(buf[:0], pk.Data, pk.Frames, f, p.Silent)
			p.Samples = buf
			win.CaptureRelease(cc, pk.Frames)
			idx += int64(pk.Frames)
			onPacket(p)
		}
	}
	return nil
}

// convert appends frames frames of data in format f as float32.
func convert(dst []float32, data []byte, frames int, f win.WaveFormat, silent bool) []float32 {
	n := frames * f.Channels
	if silent || data == nil {
		for i := 0; i < n; i++ {
			dst = append(dst, 0)
		}
		return dst
	}
	bps := f.Bits / 8
	for fr := 0; fr < frames; fr++ {
		for ch := 0; ch < f.Channels; ch++ {
			b := data[fr*f.BlockAlign+ch*bps:]
			var v float32
			switch {
			case f.Float:
				v = math.Float32frombits(binary.LittleEndian.Uint32(b))
			case f.Bits == 16:
				v = float32(int16(binary.LittleEndian.Uint16(b))) / 32768
			case f.Bits == 24:
				v = float32(int32(uint32(b[0])<<8|uint32(b[1])<<16|uint32(b[2])<<24)>>8) / 8388608
			default:
				v = float32(int32(binary.LittleEndian.Uint32(b))) / 2147483648
			}
			dst = append(dst, v)
		}
	}
	return dst
}

// HNSToQPC converts 100 ns units of QPC time (as WASAPI and Media
// Foundation report it) to QPC ticks.
func HNSToQPC(hns, freq int64) int64 {
	if freq == 10_000_000 {
		return hns
	}
	return hns/10_000_000*freq + hns%10_000_000*freq/10_000_000
}
