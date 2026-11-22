// Package displays lists the desktop's outputs with their mode and EDID name.
package displays

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupported is returned by List on operating systems without display
// enumeration support.
var ErrUnsupported = errors.New("displays: listing is not supported on this OS")

// Rect is a rectangle in desktop pixel coordinates.
type Rect struct {
	Left, Top, Right, Bottom int32
}

// Display is one output attached to the desktop.
type Display struct {
	Name         string // \\.\DISPLAYn on Windows, connector name on Linux
	ID           string // stable monitor/connector identifier across renumbering ("" if unknown)
	FriendlyName string // EDID name, e.g. "AVT GC553"
	Rect         Rect
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

// cardHints are substrings of the EDID and capture device names of common
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

// LikelyCard reports whether an EDID or device name looks like a capture card.
func LikelyCard(name string) bool { return contains(name, cardHints) }

func likelyCard(name string) bool { return LikelyCard(name) }

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

// Find returns the display whose ID or name (\\.\DISPLAYn or DISPLAYn) is
// name, or with name "auto" the single one whose EDID name looks like a
// capture card.
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
		if d.ID != "" && strings.EqualFold(d.ID, name) {
			return d, nil
		}
	}
	for _, d := range list {
		if strings.EqualFold(d.Name, name) || strings.EqualFold(strings.TrimPrefix(d.Name, `\\.\`), name) {
			return d, nil
		}
	}
	var have []string
	for _, d := range list {
		have = append(have, strings.TrimSpace(strings.TrimPrefix(d.Name, `\\.\`)+" "+d.FriendlyName))
	}
	return Display{}, fmt.Errorf("display %s not found; the displays now are: %s", name, strings.Join(have, ", "))
}
