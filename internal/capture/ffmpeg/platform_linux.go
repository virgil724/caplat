//go:build linux

package ffmpeg

import (
	"context"
	"fmt"
	"strings"

	"github.com/virgil724/caplat/capture/v4l2"
)

func defaultInputFormat() string { return "v4l2" }

// ListDevices returns the V4L2 video capture devices.
func ListDevices(_ context.Context, _ string) ([]string, error) {
	return v4l2.ListDevices()
}

// ListModes returns the modes a V4L2 device advertises via ffmpeg.
func ListModes(ctx context.Context, bin, device string) ([]Mode, error) {
	devPath := v4l2.DevicePath(device)
	out := run(ctx, bin, "-hide_banner", "-f", "v4l2", "-list_formats", "all", "-i", devPath)
	modes := ParseV4L2Modes(out)
	if len(modes) == 0 {
		return nil, fmt.Errorf("no modes listed for %q: %s", device, strings.TrimSpace(out))
	}
	return modes, nil
}
