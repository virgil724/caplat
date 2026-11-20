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
	Backend string // AudioWASAPI (default) or AudioFFmpeg
	Output  string // render endpoint, or part of its name; "" = the one that looks like the card
	Input   string // capture endpoint / DirectShow audio device; "" = the card's
	Preset  string // ffmpeg only: PresetLowLatency (default) or PresetDefault
	FFmpeg  string
	// Period asks WASAPI for an engine period shorter than the default
	// 10 ms (IAudioClient3), if the driver allows it; 0 = default.
	Period time.Duration
	// Exclusive captures in WASAPI exclusive mode (the device's minimum
	// period unless Period is set).
	Exclusive bool
	Duration  time.Duration // 0 = until the context ends
	Interval  time.Duration // statistics window; default 10 s
	CSV       io.Writer     // optional: one row per chirp
	// OnInterval, if set, gets each window's statistics as it ends.
	OnInterval func(AudioInterval)
	// OnUpdate, if set, gets the statistics so far about once a second.
	OnUpdate func(AudioUpdate)
}

// AudioUpdate is the state of a running audio measurement.
type AudioUpdate struct {
	Elapsed  time.Duration
	Second   Stats      // delivered latency of the chirps paired in the last second
	Total    Stats      // delivered latency so far
	Device   Stats      // to the driver's timestamp so far (wasapi only)
	Hist     *Histogram // of the delivered latency so far (a copy)
	Emitted  int
	Detected int
	Matched  int
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
	InputPeriod            time.Duration // WASAPI engine period used; 0 = default (about 10 ms)
	FFmpegArgs             []string
	Elapsed                time.Duration
	Delivered, Device      Stats
	Emitted, Detected      int
	Matched                int
	Intervals              []AudioInterval
}
