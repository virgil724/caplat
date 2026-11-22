//go:build !windows && !linux

package ffmpeg

import (
	"context"
	"errors"
)

func defaultInputFormat() string { return "v4l2" }

// ListDevices is not supported on this OS.
func ListDevices(context.Context, string) ([]string, error) {
	return nil, errors.New("ffmpeg device listing is not supported on this OS")
}

// ListModes is not supported on this OS.
func ListModes(context.Context, string, string) ([]Mode, error) {
	return nil, errors.New("ffmpeg mode listing is not supported on this OS")
}
