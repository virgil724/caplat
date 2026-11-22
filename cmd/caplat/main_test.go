package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/virgil724/caplat/pkg/caplat"
)

func TestFprintModes(t *testing.T) {
	var buf bytes.Buffer
	fprintModes(&buf, []caplat.Mode{
		{Format: "nv12", MinW: 1920, MinH: 1080, MaxW: 1920, MaxH: 1080, MaxFPS: 60},
		{Format: "nv12", MinW: 1920, MinH: 1080, MaxW: 1920, MaxH: 1080, MaxFPS: 60},
		{Format: "nv12", MinW: 1280, MinH: 720, MaxW: 1280, MaxH: 720, MaxFPS: 59.94006},
		{Format: "mjpeg", MinW: 3840, MinH: 2160, MaxW: 3840, MaxH: 2160, MaxFPS: 0},
		{Format: "mjpeg", MinW: 640, MinH: 360, MaxW: 1920, MaxH: 1080, MaxFPS: 0},
	})
	got := buf.String()
	want := "      nv12     1920x1080@60  1280x720@59.94\n" +
		"      mjpeg    3840x2160@?  640x360-1920x1080@?\n"
	if got != want {
		t.Fatalf("fprintModes:\n%q\nwant:\n%q", got, want)
	}
	if strings.Contains(got, "@0") {
		t.Errorf("unexpected @0 in output: %q", got)
	}
}
