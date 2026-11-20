package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strings"
	"sync"
)

// FFmpegRate is the rate ffmpeg resamples to (a no-op for 48 kHz cards).
const FFmpegRate = 48000

// FFmpegConfig describes an ffmpeg DirectShow audio capture.
type FFmpegConfig struct {
	FFmpeg string // binary; "" = ffmpeg on PATH
	Device string // DirectShow audio device
	// LowLatency adds -fflags nobuffer -flags low_delay -probesize 32
	// -analyzeduration 0 -audio_buffer_size 10; otherwise ffmpeg's defaults.
	LowLatency bool
}

// FFmpegArgs returns ffmpeg's arguments. Output is mono float at 48 kHz,
// flushed every packet so the pipe adds no buffering of its own.
func FFmpegArgs(c FFmpegConfig) []string {
	a := []string{"-hide_banner", "-nostdin", "-loglevel", "error"}
	if c.LowLatency {
		a = append(a, "-fflags", "nobuffer", "-flags", "low_delay", "-probesize", "32", "-analyzeduration", "0",
			"-audio_buffer_size", "10")
	}
	a = append(a, "-f", "dshow", "-i", "audio="+c.Device,
		"-ac", "1", "-ar", fmt.Sprint(FFmpegRate), "-f", "f32le", "-flush_packets", "1", "pipe:1")
	return a
}

// FFmpegCapture reads audio from an ffmpeg subprocess.
type FFmpegCapture struct {
	cmd      *exec.Cmd
	done     chan error
	stopOnce sync.Once
	cancel   context.CancelFunc
	Args     []string
}

// Rate is the capture's sample rate.
func (c *FFmpegCapture) Rate() int { return FFmpegRate }

// Done delivers why the capture ended.
func (c *FFmpegCapture) Done() <-chan error { return c.done }

// Stop ends ffmpeg.
func (c *FFmpegCapture) Stop() { c.stopOnce.Do(c.cancel) }

// StartFFmpeg starts ffmpeg; onSamples runs on the reading goroutine with
// packets that have no device timestamp.
func StartFFmpeg(ctx context.Context, cfg FFmpegConfig, now func() int64, onSamples OnSamples) (*FFmpegCapture, error) {
	bin := cfg.FFmpeg
	if bin == "" {
		bin = "ffmpeg"
	}
	ctx, cancel := context.WithCancel(ctx)
	c := &FFmpegCapture{done: make(chan error, 1), cancel: cancel, Args: FFmpegArgs(cfg)}
	c.cmd = exec.CommandContext(ctx, bin, c.Args...)
	var stderr bytes.Buffer
	c.cmd.Stderr = &stderr
	out, err := c.cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := c.cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	go func() {
		buf := make([]byte, 4096)
		var carry []byte
		var idx int64
		var mono []float64
		var rerr error
		for {
			n, err := out.Read(buf)
			arrive := now()
			if n > 0 {
				data := append(carry, buf[:n]...)
				whole := len(data) / 4 * 4
				mono = mono[:0]
				for i := 0; i < whole; i += 4 {
					mono = append(mono, float64(math.Float32frombits(binary.LittleEndian.Uint32(data[i:]))))
				}
				carry = append(carry[:0], data[whole:]...)
				if len(mono) > 0 {
					onSamples(mono, Packet{Start: idx, Frames: len(mono), Arrive: arrive})
					idx += int64(len(mono))
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					rerr = err
				}
				break
			}
		}
		werr := c.cmd.Wait()
		if ctx.Err() != nil {
			c.done <- nil
			return
		}
		msg := strings.TrimSpace(stderr.String())
		c.done <- fmt.Errorf("ffmpeg exited: %v %v %s", werr, rerr, msg)
	}()
	return c, nil
}
