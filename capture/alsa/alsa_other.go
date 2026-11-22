//go:build !linux

package alsa

// ListDevices is only supported on Linux.
func ListDevices() ([]DeviceInfo, error) { return nil, ErrUnsupported }

// Inputs is only supported on Linux.
func Inputs() ([]string, error) { return nil, ErrUnsupported }

// Outputs is only supported on Linux.
func Outputs() ([]string, error) { return nil, ErrUnsupported }

// Start is only supported on Linux.
func Start(Config, func() int64, int64, func(Packet)) (*Capture, error) {
	return nil, ErrUnsupported
}

// OpenPlayback is only supported on Linux.
func OpenPlayback(PlaybackConfig) (*Playback, error) {
	return nil, ErrUnsupported
}
