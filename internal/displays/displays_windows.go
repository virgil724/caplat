//go:build windows

// Package displays lists the desktop's outputs with their mode and EDID name.
package displays

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/virgil724/caplat/internal/win"
)

// Display is one output attached to the desktop.
type Display struct {
	Name         string // \\.\DISPLAYn
	FriendlyName string // EDID name, e.g. "AVT GC553"
	Rect         win.RECT
	Width        int // physical pixels
	Height       int
	RefreshNum   uint32 // refresh rate as a fraction, e.g. 60000/1001
	RefreshDen   uint32
	Primary      bool
	LikelyCard   bool // the EDID name looks like a capture card
}

// RefreshHz returns the refresh rate in Hz (0 if unknown).
func (d Display) RefreshHz() float64 {
	if d.RefreshDen == 0 {
		return 0
	}
	return float64(d.RefreshNum) / float64(d.RefreshDen)
}

// FrameRate returns the refresh rate as ffmpeg expects it: "60" or "60000/1001".
func (d Display) FrameRate() string {
	if d.RefreshDen == 0 {
		return ""
	}
	if d.RefreshNum%d.RefreshDen == 0 {
		return fmt.Sprint(d.RefreshNum / d.RefreshDen)
	}
	return fmt.Sprintf("%d/%d", d.RefreshNum, d.RefreshDen)
}

// Mode describes the output mode, e.g. "3840x2160@60.000".
func (d Display) Mode() string {
	return fmt.Sprintf("%dx%d@%.3f", d.Width, d.Height, d.RefreshHz())
}

// cardHints are substrings of the EDID and DirectShow names of common
// capture cards.
var cardHints = []string{"gc5", "live gamer", "avt", "avermedia", "cam link", "hd60", "4k60", "elgato",
	"usb3 video", "usb video", "ms2109", "ms2130", "capture", "magewell", "game capture"}

// virtualHints are substrings of virtual (software) cameras' names.
var virtualHints = []string{"virtual", "虛擬", "虚拟", "obs", "vtube", "streaming center", "camo",
	"nvidia broadcast", "snap camera", "xsplit", "droidcam", "iriun"}

func contains(name string, hints []string) bool {
	n := strings.ToLower(name)
	for _, h := range hints {
		if strings.Contains(n, h) {
			return true
		}
	}
	return false
}

func likelyCard(name string) bool { return contains(name, cardHints) }

// PickDevice chooses the capture device to measure: the one whose name looks
// like a capture card, else the only one that is not a virtual camera.
// It returns "" when that is ambiguous.
func PickDevice(devices []string) string {
	var cards, real []string
	for _, d := range devices {
		if contains(d, virtualHints) {
			continue
		}
		real = append(real, d)
		if likelyCard(d) {
			cards = append(cards, d)
		}
	}
	switch {
	case len(cards) == 1:
		return cards[0]
	case len(cards) == 0 && len(real) == 1:
		return real[0]
	}
	return ""
}

// List returns the outputs attached to the desktop. Call win.SetDPIAware
// first so sizes are physical pixels.
func List() ([]Display, error) {
	f, err := win.CreateFactory()
	if err != nil {
		return nil, fmt.Errorf("DXGI factory: %w", err)
	}
	defer f.Release()
	paths, _ := win.ActiveDisplayPaths()
	byName := map[string]win.DisplayPath{}
	for _, p := range paths {
		byName[p.GDIName] = p
	}
	var out []Display
	for i := uint32(0); ; i++ {
		ad, err := f.EnumAdapters1(i)
		if errors.Is(err, win.DXGI_ERROR_NOT_FOUND) {
			break
		}
		if err != nil {
			return nil, err
		}
		for j := uint32(0); ; j++ {
			o, err := win.EnumOutputs(ad, j)
			if err != nil {
				break
			}
			desc, err := win.OutputGetDesc(o)
			o.Release()
			if err != nil || desc.AttachedToDesktop == 0 {
				continue
			}
			r := desc.DesktopCoordinates
			d := Display{
				Name:    windows.UTF16ToString(desc.DeviceName[:]),
				Rect:    r,
				Width:   int(r.Right - r.Left),
				Height:  int(r.Bottom - r.Top),
				Primary: r.Left == 0 && r.Top == 0,
			}
			if p, ok := byName[d.Name]; ok {
				d.FriendlyName = p.FriendlyName
				d.RefreshNum, d.RefreshDen = p.Refresh.Numerator, p.Refresh.Denominator
			}
			d.LikelyCard = likelyCard(d.FriendlyName)
			out = append(out, d)
		}
		ad.Release()
	}
	return out, nil
}

// Find returns the display named name, or with name "auto" the single one
// whose EDID name looks like a capture card.
func Find(list []Display, name string) (Display, error) {
	if strings.EqualFold(name, "auto") || name == "" {
		var cards []Display
		for _, d := range list {
			if d.LikelyCard && !d.Primary {
				cards = append(cards, d)
			}
		}
		switch len(cards) {
		case 1:
			return cards[0], nil
		case 0:
			return Display{}, fmt.Errorf("no display looks like a capture card; pass --display (see `caplat list`)")
		}
		return Display{}, fmt.Errorf("several displays look like capture cards; pass --display (see `caplat list`)")
	}
	for _, d := range list {
		if strings.EqualFold(d.Name, name) || strings.EqualFold(strings.TrimPrefix(d.Name, `\\.\`), name) {
			return d, nil
		}
	}
	return Display{}, fmt.Errorf("display %q not found (see `caplat list`)", name)
}
