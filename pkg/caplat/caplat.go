// Package caplat measures a capture card's latency: it shows a frame-counter
// barcode on the output that feeds the card, takes each frame's scan-out
// vblank time from DXGI, reads the card back and times when each frame
// arrives. The CLI (cmd/caplat) and the GUI (gui/) both drive it.
package caplat

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/virgil724/caplat/internal/measure"
)

// Stats, Counts and Histogram are re-exported for callers.
type (
	Stats     = measure.Stats
	Counts    = measure.Counts
	Histogram = measure.Histogram
)

// Presets for the ffmpeg capture backend.
const (
	PresetLowLatency = "low-latency"
	PresetDefault    = "default"
)

// Capture backends.
const (
	// BackendFFmpeg reads the card through an ffmpeg subprocess and
	// DirectShow, as most streaming software does.
	BackendFFmpeg = "ffmpeg"
	// BackendMF reads it in-process with Media Foundation, and also reports
	// the driver's capture timestamp of every frame.
	BackendMF = "mf"
	// BackendDShow reads it in-process with DirectShow (capture filter ->
	// SampleGrabber -> NullRenderer, no graph clock), without ffmpeg.
	BackendDShow = "dshow"
)

// ErrPrimary is returned when the chosen display is the primary one and
// Force is not set: the measurement window would cover the desktop.
var ErrPrimary = errors.New("the chosen display is the primary display; the full-screen barcode would cover your desktop (use --force to do it anyway)")

// Config describes a measurement.
type Config struct {
	Backend       string // BackendMF (default), BackendDShow or BackendFFmpeg
	Display       string // \\.\DISPLAYn, DISPLAYn, or "auto"
	Device        string // capture device; "" = the capture card, if it is clear which
	CaptureWidth  int    // default 1280
	CaptureHeight int    // default 720
	PixelFormat   string // "auto" (default), nv12, yuyv422, mjpeg, ...
	Preset        string // ffmpeg only: PresetLowLatency (default) or PresetDefault
	FFmpeg        string // ffmpeg binary; "" = ffmpeg on PATH
	FFmpegIn      []string
	FFmpegOut     []string
	Duration      time.Duration // 0 = until the context ends or the user aborts
	Interval      time.Duration // statistics window; default 1 minute
	Warmup        time.Duration // default 2 s
	Force         bool          // allow the primary display
	CSV           io.Writer     // optional: one row per matched frame
}

func (c *Config) defaults() {
	if c.CaptureWidth == 0 || c.CaptureHeight == 0 {
		c.CaptureWidth, c.CaptureHeight = 1280, 720
	}
	if c.PixelFormat == "" {
		c.PixelFormat = "auto"
	}
	if c.Preset == "" {
		c.Preset = PresetLowLatency
	}
	if c.Backend == "" {
		c.Backend = BackendMF
	}
	if c.Interval <= 0 {
		c.Interval = time.Minute
	}
	if c.Warmup <= 0 {
		c.Warmup = 2 * time.Second
	}
}

// Setup is what a run resolved before starting.
type Setup struct {
	Display     string // \\.\DISPLAYn
	DisplayName string // EDID name
	OutputMode  string // e.g. 3840x2160@60.000
	Device      string
	Backend     string
	CaptureMode string // e.g. 1280x720@60 nv12
	Preset      string // ffmpeg only
	FFmpegArgs  []string
}

// Interval is the statistics of one window.
type Interval struct {
	Start, End time.Duration // since measuring started
	Stats      Stats
	Device     Stats // vblank -> driver timestamp (mf only)
}

// Update is sent about once a second while measuring.
type Update struct {
	Elapsed  time.Duration
	Second   Stats      // the last second
	Total    Stats      // everything so far
	Counts   Counts     // unmatched frames so far
	Interval *Interval  // set when a window closed
	Hist     *Histogram // a copy of the total histogram, owned by the receiver
	Device   Stats      // vblank -> driver timestamp so far (mf only)
}

// Result is the outcome of a run.
type Result struct {
	Setup     Setup
	Total     Stats
	Counts    Counts
	Intervals []Interval
	Hist      Histogram
	Device    Stats // vblank -> driver timestamp (mf only; N = 0 otherwise)
	Elapsed   time.Duration
}

// FormatStats renders stats as one line.
func FormatStats(s Stats) string {
	if s.N == 0 {
		return "n=0"
	}
	return fmt.Sprintf("n=%d  min %.1f  p10 %.1f  p50 %.1f  p90 %.1f  p99 %.1f  max %.1f  mean %.1f ms",
		s.N, s.Min, s.P10, s.P50, s.P90, s.P99, s.Max, s.Mean)
}
