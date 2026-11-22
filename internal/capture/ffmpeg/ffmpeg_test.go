package ffmpeg

import (
	"strings"
	"testing"
)

func TestArgsLowLatency(t *testing.T) {
	a, err := Args(Config{Device: "Live Gamer Ultra-Video", Width: 1280, Height: 720,
		FrameRate: "60000/1001", PixelFormat: "nv12", Preset: PresetLowLatency, InputFormat: "dshow"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(a, " ")
	for _, want := range []string{
		"-fflags nobuffer -flags low_delay -probesize 32 -analyzeduration 0 -rtbufsize 6M -f dshow",
		"-video_size 1280x720 -framerate 60000/1001 -pixel_format nv12 -i video=Live Gamer Ultra-Video",
		"-fps_mode passthrough -f rawvideo -pix_fmt nv12 pipe:1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

func TestArgsDefaultAndMJPEG(t *testing.T) {
	a, _ := Args(Config{Device: "Cam", Width: 1920, Height: 1080, FrameRate: "60",
		PixelFormat: "mjpeg", Preset: PresetDefault, InputFormat: "dshow", ExtraIn: []string{"-x", "1"}, ExtraOut: []string{"-y", "2"}})
	got := strings.Join(a, " ")
	if strings.Contains(got, "nobuffer") || strings.Contains(got, "passthrough") {
		t.Errorf("default preset added low-latency options: %s", got)
	}
	for _, want := range []string{"-x 1 -f dshow", "-vcodec mjpeg -i video=Cam -y 2 -f rawvideo -pix_fmt gray"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if _, err := Args(Config{Device: "Cam", Width: 1, Height: 1, Preset: "bogus"}); err == nil {
		t.Error("unknown preset accepted")
	}
}

const listOptions = `[dshow @ 0] DirectShow video device options (from video devices)
[dshow @ 0]  Pin "Capture" (alternative pin name "0")
[dshow @ 0]   pixel_format=nv12  min s=1280x720 fps=25 max s=1280x720 fps=60.0002
[dshow @ 0]   pixel_format=nv12  min s=1280x720 fps=25 max s=1280x720 fps=60.0002 (tv, bt709/bt709/unknown, topleft)
[dshow @ 0]   pixel_format=yuyv422  min s=1920x1080 fps=24 max s=1920x1080 fps=60.0002
[dshow @ 0]   pixel_format=nv12  min s=1920x1080 fps=24 max s=1920x1080 fps=120
[dshow @ 0]   vcodec=mjpeg  min s=3840x2160 fps=5 max s=3840x2160 fps=30
`

func TestParseModesAndPick(t *testing.T) {
	modes := ParseModes(listOptions)
	if len(modes) != 4 {
		t.Fatalf("got %d modes: %+v", len(modes), modes)
	}
	cases := []struct {
		w, h int
		fps  float64
		want string
	}{
		{1280, 720, 60, "nv12"},
		{1920, 1080, 120, "nv12"},
		{1920, 1080, 59.94, "nv12"},
		{3840, 2160, 30, "mjpeg"},
	}
	for _, c := range cases {
		got, err := PickFormat(modes, c.w, c.h, c.fps)
		if err != nil || got != c.want {
			t.Errorf("%dx%d@%v: got %q err %v", c.w, c.h, c.fps, got, err)
		}
	}
	if _, err := PickFormat(modes, 3840, 2160, 60); err == nil {
		t.Error("4K60 should not be supported")
	}
}

func TestParseDevices(t *testing.T) {
	out := `[in#0 @ 0] "Live Gamer Ultra-Video" (video)
[in#0 @ 0]   Alternative name "@device_pnp_..."
[in#0 @ 0] "OBS Virtual Camera" (video)
[in#0 @ 0] "HDMI (Live Gamer Ultra-Audio)" (audio)`
	devs := ParseDevices(out)
	if len(devs) != 2 || devs[0] != "Live Gamer Ultra-Video" || devs[1] != "OBS Virtual Camera" {
		t.Fatalf("%v", devs)
	}
}
