// Package capture holds what the capture backends share.
package capture

// Frame is one captured picture.
type Frame struct {
	// Y is the luma plane: at least Stride*Height bytes. For YUY2 it is a
	// converted copy. Valid only during the callback.
	Y      []byte
	Stride int // bytes per row of Y
	// Data is the picture as the device delivered it, every plane, rows
	// Pitch bytes apart (for NV12: Height rows of Y, then Height/2 rows of
	// interleaved UV). Nil when the backend only has luma (ffmpeg). Valid
	// only during the callback.
	Data          []byte
	Pitch         int
	FourCC        string // e.g. "NV12", "YUY2"
	Width, Height int
	// QPC is when the whole frame reached caplat, in platform monotonic ticks
	// (QPC on Windows, CLOCK_MONOTONIC ns on Linux).
	QPC int64
	// DeviceQPC is the driver's capture timestamp converted to platform
	// monotonic ticks (QPC on Windows, CLOCK_MONOTONIC ns on Linux), or 0
	// when the backend does not have one (ffmpeg).
	DeviceQPC int64
}

// Source is a running capture.
type Source interface {
	// Done delivers the reason the capture stopped (once).
	Done() <-chan error
	// Stop ends the capture.
	Stop()
}
