package ffmpeg

import (
	"strings"
	"testing"
)

func TestArgsV4L2LowLatencyAndUnwrap(t *testing.T) {
	a, err := Args(Config{
		InputFormat: "v4l2",
		Device:      "Live Gamer Ultra-Video (/dev/video2)",
		Width:       1280,
		Height:      720,
		FrameRate:   "60000/1001",
		PixelFormat: "nv12",
		Preset:      PresetLowLatency,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(a, " ")
	if strings.Contains(got, "rtbufsize") || strings.Contains(got, "dshow") {
		t.Errorf("v4l2 args should not contain dshow options: %s", got)
	}
	for _, want := range []string{
		"-fflags nobuffer -flags low_delay -probesize 32 -analyzeduration 0 -f v4l2",
		"-video_size 1280x720 -framerate 60000/1001 -input_format nv12 -i /dev/video2",
		"-fps_mode passthrough -f rawvideo -pix_fmt nv12 pipe:1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

func TestArgsV4L2DefaultAndMJPEG(t *testing.T) {
	a, err := Args(Config{
		InputFormat: "v4l2",
		Device:      "/dev/video0",
		Width:       1920,
		Height:      1080,
		FrameRate:   "60",
		PixelFormat: "mjpeg",
		Preset:      PresetDefault,
		ExtraIn:     []string{"-thread_queue_size", "8"},
		ExtraOut:    []string{"-an"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(a, " ")
	if strings.Contains(got, "nobuffer") || strings.Contains(got, "passthrough") {
		t.Errorf("default preset added low-latency options: %s", got)
	}
	for _, want := range []string{
		"-thread_queue_size 8 -f v4l2",
		"-input_format mjpeg -i /dev/video0 -an -f rawvideo -pix_fmt gray pipe:1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

func TestParseV4L2ModesAndPick(t *testing.T) {
	const v4l2Out = `[video4linux2,v4l2 @ 0x55a1] Raw       :     yuyv422 :           YUYV 4:2:2 : 1920x1080 1280x720 640x480
[video4linux2,v4l2 @ 0x55a1] Raw       :        nv12 :         Y/CbCr 4:2:0 : 1920x1080 1280x720
[video4linux2,v4l2 @ 0x55a1] Compressed:       mjpeg :            Motion-JPEG : 3840x2160 1920x1080
[video4linux2,v4l2 @ 0x55a1] Raw       :     yuv420p :         Planar YUV 4:2:0 : {32-1280, 2}x{32-720, 2}
`
	modes := ParseV4L2Modes(v4l2Out)
	if len(modes) != 8 {
		t.Fatalf("got %d modes: %+v", len(modes), modes)
	}
	got, err := PickFormat(modes, 1280, 720, 60)
	if err != nil || got != "nv12" {
		t.Errorf("PickFormat(1280x720@60) = (%q, %v), want nv12", got, err)
	}
	got, err = PickFormat(modes, 3840, 2160, 60)
	if err != nil || got != "mjpeg" {
		t.Errorf("PickFormat(3840x2160@60) = (%q, %v), want mjpeg", got, err)
	}
	got, err = PickFormat(modes, 640, 480, 30)
	if err != nil || got != "yuyv422" {
		t.Errorf("PickFormat(640x480@30) = (%q, %v), want yuyv422", got, err)
	}
	got, err = PickFormat(modes, 800, 600, 60)
	if err != nil || got != "yuv420p" {
		t.Errorf("PickFormat(800x600@60 stepwise) = (%q, %v), want yuv420p", got, err)
	}
}
