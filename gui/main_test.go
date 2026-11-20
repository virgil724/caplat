package main

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/virgil724/caplat/pkg/caplat"
)

var testDisplays = []caplat.Display{
	{Name: `\\.\DISPLAY1`, FriendlyName: "M27Q", Width: 2560, Height: 1440, RefreshNum: 165, RefreshDen: 1, Primary: true},
	{Name: `\\.\DISPLAY3`, FriendlyName: "AVT GC553", Width: 3840, Height: 2160, RefreshNum: 60, RefreshDen: 1, LikelyCard: true},
}

var testDevices = []string{"OBS Virtual Camera", "Live Gamer Ultra-Video"}

// The capture card's output and device are chosen by default.
func TestDefaultsPickTheCard(t *testing.T) {
	a := newApp()
	a.loaded(testDisplays, testDevices, nil)
	tt := ui.NewTester(a.view, 1100, 760)
	if !tt.HasText("Live Gamer Ultra-Video") {
		t.Errorf("device not chosen; texts %q", tt.Texts())
	}
	d, ok := a.chosenDisplay()
	if !ok || d.Name != `\\.\DISPLAY3` {
		t.Errorf("display %+v", d)
	}
	cfg, err := a.config()
	if err != nil || cfg.Device != "Live Gamer Ultra-Video" || cfg.CaptureWidth != 1280 || cfg.Force {
		t.Errorf("config %+v err %v", cfg, err)
	}
}

// Choosing the primary display asks before covering it.
func TestPrimaryAsksFirst(t *testing.T) {
	a := newApp()
	a.loaded(testDisplays, testDevices, nil)
	a.display = displayLabel(testDisplays[0])
	a.start(false)
	if !a.askPrimary || a.running {
		t.Fatalf("askPrimary %v running %v", a.askPrimary, a.running)
	}
	tt := ui.NewTester(a.view, 1100, 760)
	if !tt.HasText("Measure on the primary display?") {
		t.Errorf("no confirmation; texts %q", tt.Texts())
	}
}

func TestLoadErrorShown(t *testing.T) {
	a := newApp()
	a.loaded(nil, nil, errors.New("ffmpeg not found"))
	tt := ui.NewTester(a.view, 1100, 760)
	if !tt.HasText("ffmpeg not found") {
		t.Errorf("texts %q", tt.Texts())
	}
}
