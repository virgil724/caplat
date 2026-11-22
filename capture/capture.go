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

	hold func() func()
}

// Hold retains the frame's zero-copy driver buffer (Data, and Y when
// !NeedsLumaBuffer(FourCC)) beyond the capture callback without copying.
// The caller must invoke the returned release function once when finished
// reading the buffer (safe to call from another goroutine and idempotent).
//
// Hold returns nil if the backend does not support zero-copy holding, if
// called after the callback has returned, or if all holdable buffers are
// currently held (in which case the caller should drop the frame or copy it
// so the driver queue does not starve).
func (f Frame) Hold() (release func()) {
	if f.hold == nil {
		return nil
	}
	return f.hold()
}

// WithHold returns a copy of f with hold attached for Frame.Hold. It is
// intended for capture backends.
func WithHold(f Frame, hold func() func()) Frame {
	f.hold = hold
	return f
}

// Source is a running capture.
type Source interface {
	// Done delivers the reason the capture stopped (once).
	Done() <-chan error
	// Stop ends the capture.
	Stop()
}
