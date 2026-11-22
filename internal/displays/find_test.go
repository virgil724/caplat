package displays

import (
	"math"
	"strings"
	"testing"
)

func TestFindByIDAndNotFound(t *testing.T) {
	list := []Display{
		{Name: `\\.\DISPLAY3`, ID: `\\?\DISPLAY#MSI3CC0#1`, FriendlyName: "MAG274QRF-QD", Primary: true},
		{Name: `\\.\DISPLAY1`, ID: `\\?\DISPLAY#AVT0553#1`, FriendlyName: "AVT GC553", LikelyCard: true},
	}
	d, err := Find(list, `\\?\display#avt0553#1`)
	if err != nil || d.Name != `\\.\DISPLAY1` {
		t.Fatalf("by ID: %+v %v", d, err)
	}
	if d, err := Find(list, "DISPLAY3"); err != nil || !d.Primary {
		t.Fatalf("by name: %+v %v", d, err)
	}
	if d, err := Find(list, "auto"); err != nil || d.Name != `\\.\DISPLAY1` {
		t.Fatalf("auto: %+v %v", d, err)
	}
	if d, err := Find(list, ""); err != nil || d.Name != `\\.\DISPLAY1` {
		t.Fatalf("empty (auto): %+v %v", d, err)
	}
	_, err = Find(list, `\\.\DISPLAY2`)
	if err == nil || !strings.Contains(err.Error(), "DISPLAY1 AVT GC553") || strings.Contains(err.Error(), `\\\\`) {
		t.Fatalf("not found error: %v", err)
	}
}

func TestFindAutoEdgeCases(t *testing.T) {
	// Primary card is ignored in auto mode.
	onlyPrimaryCard := []Display{
		{Name: "DP-1", FriendlyName: "AVT GC553", Primary: true, LikelyCard: true},
	}
	if _, err := Find(onlyPrimaryCard, "auto"); err == nil || !strings.Contains(err.Error(), "no display looks like a capture card") {
		t.Fatalf("expected no card error, got %v", err)
	}

	// Multiple non-primary cards are ambiguous.
	twoCards := []Display{
		{Name: "DP-1", FriendlyName: "AVT GC553", LikelyCard: true},
		{Name: "HDMI-A-1", FriendlyName: "Elgato HD60", LikelyCard: true},
	}
	if _, err := Find(twoCards, "auto"); err == nil || !strings.Contains(err.Error(), "several displays look like capture cards") {
		t.Fatalf("expected ambiguous error, got %v", err)
	}
}

func TestDisplayModeAndRefresh(t *testing.T) {
	d := Display{Width: 3840, Height: 2160, RefreshNum: 60000, RefreshDen: 1001}
	if math.Abs(d.RefreshHz()-59.94005994) > 1e-6 {
		t.Errorf("RefreshHz() = %v", d.RefreshHz())
	}
	if got := d.FrameRate(); got != "60000/1001" {
		t.Errorf("FrameRate() = %q, want 60000/1001", got)
	}
	if got := d.Mode(); got != "3840x2160@59.940" {
		t.Errorf("Mode() = %q", got)
	}

	d60 := Display{Width: 1920, Height: 1080, RefreshNum: 60, RefreshDen: 1}
	if got := d60.FrameRate(); got != "60" {
		t.Errorf("FrameRate() = %q, want 60", got)
	}
	if got := d60.Mode(); got != "1920x1080@60.000" {
		t.Errorf("Mode() = %q", got)
	}

	var zero Display
	if zero.RefreshHz() != 0 || zero.FrameRate() != "" {
		t.Errorf("zero display: hz=%v fr=%q", zero.RefreshHz(), zero.FrameRate())
	}
}
