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

// testApp is an app that lists ds as the displays, never the real ones.
func testApp(ds []caplat.Display) *app {
	a := newApp()
	a.listDisplays = func() ([]caplat.Display, error) { return ds, nil }
	a.loaded(ds, testDevices, nil)
	return a
}

// Windows renumbers displays; the chosen one is followed by its ID.
func TestDisplayFollowedAfterRenumbering(t *testing.T) {
	before := []caplat.Display{
		{Name: `\\.\DISPLAY2`, ID: "mon-mag", FriendlyName: "MAG274QRF-QD", Primary: true},
		{Name: `\\.\DISPLAY3`, ID: "mon-gc553", FriendlyName: "AVT GC553", LikelyCard: true},
	}
	after := []caplat.Display{
		{Name: `\\.\DISPLAY3`, ID: "mon-mag", FriendlyName: "MAG274QRF-QD", Primary: true},
		{Name: `\\.\DISPLAY1`, ID: "mon-gc553", FriendlyName: "AVT GC553", LikelyCard: true},
	}
	a := testApp(before)
	a.listDisplays = func() ([]caplat.Display, error) { return after, nil }
	a.refreshDisplays()
	d, ok := a.chosenDisplay()
	if !ok || d.Name != `\\.\DISPLAY1` || d.Primary {
		t.Fatalf("chose %+v", d)
	}
	if cfg, err := a.config(); err != nil || cfg.Display != "mon-gc553" || cfg.Force {
		t.Errorf("config %+v err %v", cfg, err)
	}
}

// The capture card's output and device are chosen by default.
func TestDefaultsPickTheCard(t *testing.T) {
	a := testApp(testDisplays)
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
	a := testApp(testDisplays)
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
