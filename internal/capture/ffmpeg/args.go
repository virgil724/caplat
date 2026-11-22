// Package ffmpeg reads a DirectShow capture device through an ffmpeg
// subprocess, as most streaming software does, so the measurement includes
// what ffmpeg's own buffering adds.
package ffmpeg

import (
	"fmt"
	"strings"

	"github.com/virgil724/caplat/capture/v4l2"
)

// Presets select ffmpeg's buffering behaviour.
const (
	// PresetLowLatency disables input probing and demux buffering and passes
	// frames through unchanged. Probing reads ahead while the device opens,
	// and that backlog never drains because frames are then read exactly as
	// fast as they are produced.
	PresetLowLatency = "low-latency"
	// PresetDefault adds nothing: what plain `ffmpeg -f dshow` does.
	PresetDefault = "default"
)

// Config describes one capture.
type Config struct {
	FFmpeg      string // binary; "" = ffmpeg on PATH
	InputFormat string // "dshow" (Windows default) or "v4l2" (Linux default)
	Device      string // DirectShow video device name or V4L2 device path/name
	Width       int
	Height      int
	FrameRate   string // "60", "60000/1001"
	PixelFormat string // nv12, yuyv422, mjpeg, ... ("" = let the device pick)
	Preset      string
	ExtraIn     []string // inserted before -f <InputFormat>
	ExtraOut    []string // inserted after -i
}

// presetIn and presetOut return the preset's input and output options.
func presetIn(p, inFmt string, frameBytes int) ([]string, error) {
	switch p {
	case PresetLowLatency, "":
		args := []string{"-fflags", "nobuffer", "-flags", "low_delay",
			"-probesize", "32", "-analyzeduration", "0"}
		if inFmt == "dshow" {
			// ~3 frames of real-time buffer, at least 4 MB (dshow demuxer option).
			mb := max(4, (frameBytes*3+1<<20-1)/(1<<20))
			args = append(args, "-rtbufsize", fmt.Sprintf("%dM", mb))
		}
		return args, nil
	case PresetDefault:
		return nil, nil
	}
	return nil, fmt.Errorf("unknown preset %q (want %s or %s)", p, PresetLowLatency, PresetDefault)
}

func presetOut(p string) []string {
	if p == PresetDefault {
		return nil
	}
	return []string{"-fps_mode", "passthrough"}
}

// OutputFormat is the raw format ffmpeg writes for an input format, and
// FrameBytes its size. Planar 4:2:0 input (nv12, yuv420p) is passed through
// untouched and only its leading Y plane is read: converting it, even to
// gray, costs ffmpeg about half a millisecond per frame. Anything else is
// converted to 8-bit luma (gray).
func OutputFormat(input string) string {
	switch strings.ToLower(input) {
	case "nv12", "yuv420p":
		return strings.ToLower(input)
	}
	return "gray"
}

// FrameBytes is the size of one output frame; the luma plane is its first
// w*h bytes.
func FrameBytes(input string, w, h int) int {
	if OutputFormat(input) == "gray" {
		return w * h
	}
	return w * h * 3 / 2
}

// Args returns the ffmpeg arguments (without the binary). Frames come out raw
// on stdout in OutputFormat(PixelFormat); the barcode only needs the luma.
func Args(c Config) ([]string, error) {
	if c.Device == "" {
		return nil, fmt.Errorf("no capture device")
	}
	if c.Width <= 0 || c.Height <= 0 {
		return nil, fmt.Errorf("bad capture size %dx%d", c.Width, c.Height)
	}
	inFmt := strings.ToLower(c.InputFormat)
	if inFmt == "" {
		inFmt = defaultInputFormat()
	}
	in, err := presetIn(c.Preset, inFmt, c.Width*c.Height*2)
	if err != nil {
		return nil, err
	}
	a := []string{"-hide_banner", "-nostdin", "-loglevel", "error"}
	a = append(a, in...)
	a = append(a, c.ExtraIn...)
	switch inFmt {
	case "dshow":
		a = append(a, "-f", "dshow",
			"-video_size", fmt.Sprintf("%dx%d", c.Width, c.Height))
		if c.FrameRate != "" {
			a = append(a, "-framerate", c.FrameRate)
		}
		switch pf := strings.ToLower(c.PixelFormat); {
		case pf == "", pf == "auto":
		case IsCompressed(pf):
			a = append(a, "-vcodec", pf)
		default:
			a = append(a, "-pixel_format", pf)
		}
		a = append(a, "-i", "video="+c.Device)
	case "v4l2":
		a = append(a, "-f", "v4l2",
			"-video_size", fmt.Sprintf("%dx%d", c.Width, c.Height))
		if c.FrameRate != "" {
			a = append(a, "-framerate", c.FrameRate)
		}
		if pf := strings.ToLower(c.PixelFormat); pf != "" && pf != "auto" {
			a = append(a, "-input_format", pf)
		}
		a = append(a, "-i", v4l2.DevicePath(c.Device))
	default:
		return nil, fmt.Errorf("unknown ffmpeg input format %q", inFmt)
	}
	a = append(a, presetOut(c.Preset)...)
	a = append(a, c.ExtraOut...)
	a = append(a, "-f", "rawvideo", "-pix_fmt", OutputFormat(c.PixelFormat), "pipe:1")
	return a, nil
}

// IsCompressed reports whether a DirectShow format is a codec (selected with
// -vcodec) rather than a raw pixel format.
func IsCompressed(f string) bool {
	switch strings.ToLower(f) {
	case "mjpeg", "h264", "hevc":
		return true
	}
	return false
}
