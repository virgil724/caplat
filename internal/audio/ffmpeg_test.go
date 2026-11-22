package audio

import (
	"strings"
	"testing"
)

func TestFFmpegArgsDShow(t *testing.T) {
	a := FFmpegArgs(FFmpegConfig{
		InputFormat: "dshow",
		Device:      "HDMI (Live Gamer Ultra-Audio)",
		LowLatency:  true,
	})
	got := strings.Join(a, " ")
	want := "-hide_banner -nostdin -loglevel error -fflags nobuffer -flags low_delay -probesize 32 -analyzeduration 0 -audio_buffer_size 10 -f dshow -i audio=HDMI (Live Gamer Ultra-Audio) -ac 1 -ar 48000 -f f32le -flush_packets 1 pipe:1"
	if got != want {
		t.Errorf("got:\n  %s\nwant:\n  %s", got, want)
	}
}

func TestFFmpegArgsALSAAndPulse(t *testing.T) {
	a := FFmpegArgs(FFmpegConfig{
		InputFormat: "alsa",
		Device:      "Live Gamer ULTRA - USB Audio (hw:2,0)",
		LowLatency:  true,
	})
	got := strings.Join(a, " ")
	if strings.Contains(got, "audio_buffer_size") || strings.Contains(got, "dshow") {
		t.Errorf("alsa args contain dshow options: %s", got)
	}
	want := "-hide_banner -nostdin -loglevel error -fflags nobuffer -flags low_delay -probesize 32 -analyzeduration 0 -f alsa -i hw:2,0 -ac 1 -ar 48000 -f f32le -flush_packets 1 pipe:1"
	if got != want {
		t.Errorf("alsa got:\n  %s\nwant:\n  %s", got, want)
	}

	p := FFmpegArgs(FFmpegConfig{
		InputFormat: "pulse",
		Device:      "alsa_input.usb-AVerMedia",
		LowLatency:  false,
	})
	gotP := strings.Join(p, " ")
	wantP := "-hide_banner -nostdin -loglevel error -f pulse -i alsa_input.usb-AVerMedia -ac 1 -ar 48000 -f f32le -flush_packets 1 pipe:1"
	if gotP != wantP {
		t.Errorf("pulse got:\n  %s\nwant:\n  %s", gotP, wantP)
	}
}
