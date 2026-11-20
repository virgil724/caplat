package caplat

import (
	"io"
	"time"
)

// Audio capture backends.
const (
	// AudioWASAPI reads the card's audio in-process with WASAPI, which also
	// gives the driver's capture time of every packet.
	AudioWASAPI = "wasapi"
	// AudioFFmpeg reads it through an ffmpeg subprocess and DirectShow.
	AudioFFmpeg = "ffmpeg"
)

// AudioConfig describes an audio latency measurement: a 10 ms chirp every
// 500 ms on Output (the HDMI audio of the display feeding the card), found
// again in Input (the card's audio).
type AudioConfig struct {
	Backend  string // AudioWASAPI (default) or AudioFFmpeg
	Output   string // render endpoint, or part of its name; "" = the one that looks like the card
	Input    string // capture endpoint / DirectShow audio device; "" = the card's
	Preset   string // ffmpeg only: PresetLowLatency (default) or PresetDefault
	FFmpeg   string
	Duration time.Duration // 0 = until the context ends
	Interval time.Duration // statistics window; default 10 s
	CSV      io.Writer     // optional: one row per chirp
	// OnInterval, if set, gets each window's statistics as it ends.
	OnInterval func(AudioInterval)
}

// AudioInterval is the statistics of one window.
type AudioInterval struct {
	Start, End time.Duration
	Delivered  Stats // chirp played -> caplat has it
	Device     Stats // chirp played -> driver's capture timestamp (wasapi only)
}

// AudioResult is the outcome of an audio measurement.
type AudioResult struct {
	Output, Input, Backend string
	OutputFormat           string
	FFmpegArgs             []string
	Elapsed                time.Duration
	Delivered, Device      Stats
	Emitted, Detected      int
	Matched                int
	Intervals              []AudioInterval
}
