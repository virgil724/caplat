//go:build windows

package displays

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"

	"github.com/virgil724/caplat/internal/win"
)

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
				Rect:    Rect(r),
				Width:   int(r.Right - r.Left),
				Height:  int(r.Bottom - r.Top),
				Primary: r.Left == 0 && r.Top == 0,
			}
			if p, ok := byName[d.Name]; ok {
				d.FriendlyName = p.FriendlyName
				d.ID = p.DevicePath
				d.RefreshNum, d.RefreshDen = p.Refresh.Numerator, p.Refresh.Denominator
			}
			d.LikelyCard = likelyCard(d.FriendlyName)
			out = append(out, d)
		}
		ad.Release()
	}
	return out, nil
}
