package caplat

import (
	"testing"

	"github.com/virgil724/caplat/capture/v4l2"
)

func TestModesFromV4L2(t *testing.T) {
	got := modesFromV4L2([]v4l2.Mode{
		{FourCC: "YUYV", Width: 640, Height: 480, FPS: 30},
		{FourCC: "YUYV", Width: 640, Height: 480, FPS: 15},
		{FourCC: "MJPG", Width: 1920, Height: 1080, FPS: 60},
		{FourCC: "XYZW", Width: 320, Height: 240, FPS: 5},
	})
	if len(got) != 3 {
		t.Fatalf("got %d modes, want 3: %+v", len(got), got)
	}
	if m := got[0]; m.Format != "yuyv422" || m.MaxW != 640 || m.MinFPS != 15 || m.MaxFPS != 30 {
		t.Errorf("yuyv mode = %+v", m)
	}
	if m := got[1]; m.Format != "mjpeg" || m.MaxFPS != 60 {
		t.Errorf("mjpeg mode = %+v", m)
	}
	if got[2].Format != "xyzw" {
		t.Errorf("unknown fourcc should be lowercased, got %q", got[2].Format)
	}
}
