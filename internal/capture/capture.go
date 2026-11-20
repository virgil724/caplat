// Package capture holds what the capture backends share.
package capture

// Frame is one captured picture's luma plane.
type Frame struct {
	Y      []byte // at least Stride*height bytes; valid only during the callback
	Stride int    // bytes per row of Y
	// QPC is when the whole frame reached caplat.
	QPC int64
	// DeviceQPC is the driver's capture timestamp converted to QPC ticks,
	// or 0 when the backend does not have one (ffmpeg).
	DeviceQPC int64
}

// Source is a running capture.
type Source interface {
	// Done delivers the reason the capture stopped (once).
	Done() <-chan error
	// Stop ends the capture.
	Stop()
}
