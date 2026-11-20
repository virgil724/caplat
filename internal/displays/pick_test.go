//go:build windows

package displays

import "testing"

func TestPickDevice(t *testing.T) {
	devs := []string{"Pixel 10 Pro (Windows 虛擬攝影機)", "Live Gamer Ultra-Video", "VTubeStudioCam", "OBS Virtual Camera"}
	if got := PickDevice(devs); got != "Live Gamer Ultra-Video" {
		t.Errorf("got %q", got)
	}
	if got := PickDevice([]string{"OBS Virtual Camera", "USB Camera"}); got != "USB Camera" {
		t.Errorf("only real device: got %q", got)
	}
	if got := PickDevice([]string{"Cam A", "Cam B"}); got != "" {
		t.Errorf("ambiguous: got %q", got)
	}
}
