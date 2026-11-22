//go:build !linux

package v4l2

import "github.com/virgil724/caplat/capture"

// ListDeviceInfos is only supported on Linux.
func ListDeviceInfos() ([]DeviceInfo, error) { return nil, ErrUnsupported }

// ListDevices is only supported on Linux.
func ListDevices() ([]string, error) { return nil, ErrUnsupported }

// ListModes is only supported on Linux.
func ListModes(string) ([]Mode, error) { return nil, ErrUnsupported }

// Start is only supported on Linux.
func Start(Config, func() int64, int64, func(capture.Frame)) (*Source, error) {
	return nil, ErrUnsupported
}
