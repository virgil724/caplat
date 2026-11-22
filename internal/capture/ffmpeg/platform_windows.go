//go:build windows

package ffmpeg

import (
	"context"
	"fmt"
	"strings"
)

func defaultInputFormat() string { return "dshow" }

// ListDevices returns the DirectShow video capture devices.
func ListDevices(ctx context.Context, bin string) ([]string, error) {
	out := run(ctx, bin, "-hide_banner", "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	devs := ParseDevices(out)
	if len(devs) == 0 && !strings.Contains(out, "dshow") {
		return nil, fmt.Errorf("ffmpeg did not list DirectShow devices (is ffmpeg installed and on PATH?): %s", strings.TrimSpace(out))
	}
	return devs, nil
}

// ListModes returns the modes a device advertises.
func ListModes(ctx context.Context, bin, device string) ([]Mode, error) {
	out := run(ctx, bin, "-hide_banner", "-list_options", "true", "-f", "dshow", "-i", "video="+device)
	modes := ParseModes(out)
	if len(modes) == 0 {
		return nil, fmt.Errorf("no modes listed for %q: %s", device, strings.TrimSpace(out))
	}
	return modes, nil
}
