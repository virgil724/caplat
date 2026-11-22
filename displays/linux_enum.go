package displays

import (
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type drmConnectorState struct {
	X, Y      int32
	Mode      drmModeModeInfo
	ModeValid bool
}

type drmCard interface {
	ActiveConnectors() (map[string]drmConnectorState, error)
	Close() error
}

var ntscRates = [][2]uint32{
	{24000, 1001},
	{30000, 1001},
	{48000, 1001},
	{60000, 1001},
	{120000, 1001},
	{240000, 1001},
}

func modeRefresh(m drmModeModeInfo) (num, den uint32) {
	if m.Clock == 0 || m.HTotal == 0 || m.VTotal == 0 {
		if m.VRefresh > 0 {
			return m.VRefresh, 1
		}
		return 0, 0
	}
	rawNum := uint64(m.Clock) * 1000
	if m.Flags&drmModeInterlace != 0 {
		rawNum *= 2
	}
	rawDen := uint64(m.HTotal) * uint64(m.VTotal)
	if m.Flags&drmModeDblScan != 0 {
		rawDen *= 2
	}
	if m.VScan > 1 {
		rawDen *= uint64(m.VScan)
	}
	hz := float64(rawNum) / float64(rawDen)

	// Snap within 200 ppm to NTSC 1000/1001 rates or integer Hz to absorb
	// 10 kHz clock quantization in DRM/EDID mode timings.
	const tol = 2e-4
	for _, r := range ntscRates {
		target := float64(r[0]) / float64(r[1])
		if math.Abs(hz-target)/target <= tol {
			return r[0], r[1]
		}
	}
	nearest := math.Round(hz)
	if nearest >= 1 && math.Abs(hz-nearest)/nearest <= tol {
		return uint32(nearest), 1
	}

	g := gcd64(rawNum, rawDen)
	rawNum /= g
	rawDen /= g
	for rawNum > math.MaxUint32 || rawDen > math.MaxUint32 {
		rawNum >>= 1
		rawDen >>= 1
	}
	if rawDen == 0 {
		return m.VRefresh, 1
	}
	return uint32(rawNum), uint32(rawDen)
}

func gcd64(a, b uint64) uint64 {
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

func listLinux(sysfsDRM string, openDRM func(cardPath string) (drmCard, error)) ([]Display, error) {
	entries, err := os.ReadDir(sysfsDRM)
	if err != nil {
		return nil, err
	}
	type connectorEntry struct {
		dirName  string
		cardPart string
		connName string
	}
	var conns []connectorEntry
	for _, ent := range entries {
		cardPart, connName, ok := parseDRMConnectorDir(ent.Name())
		if !ok {
			continue
		}
		conns = append(conns, connectorEntry{dirName: ent.Name(), cardPart: cardPart, connName: connName})
	}
	sort.Slice(conns, func(i, j int) bool { return conns[i].dirName < conns[j].dirName })

	cardStates := make(map[string]map[string]drmConnectorState)
	cardQueried := make(map[string]bool)

	var out []Display
	for _, c := range conns {
		base := filepath.Join(sysfsDRM, c.dirName)
		if readTrimmed(filepath.Join(base, "status")) != "connected" {
			continue
		}
		enabledBytes, err := os.ReadFile(filepath.Join(base, "enabled"))
		hasEnabled := err == nil
		if hasEnabled && strings.TrimSpace(string(enabledBytes)) != "enabled" {
			continue
		}

		if !cardQueried[c.cardPart] {
			cardQueried[c.cardPart] = true
			if openDRM != nil {
				if card, err := openDRM("/dev/dri/" + c.cardPart); err == nil {
					if st, err := card.ActiveConnectors(); err == nil {
						cardStates[c.cardPart] = st
					}
					_ = card.Close()
				}
			}
		}

		d := Display{
			Name: c.connName,
			ID:   "drm:" + c.dirName,
		}
		if edidRaw, err := os.ReadFile(filepath.Join(base, "edid")); err == nil && len(edidRaw) >= 128 {
			if info, err := ParseEDID(edidRaw); err == nil {
				d.FriendlyName = info.FriendlyName()
				d.LikelyCard = LikelyCard(d.FriendlyName) || LikelyCard(info.Manufacturer)
				d.ID = formatEDIDID(c.connName, info, edidRaw[:128])
			}
		}

		if st, ok := cardStates[c.cardPart][c.connName]; ok && st.ModeValid && st.Mode.HDisplay > 0 && st.Mode.VDisplay > 0 {
			d.Width = int(st.Mode.HDisplay)
			d.Height = int(st.Mode.VDisplay)
			d.Rect = Rect{
				Left:   st.X,
				Top:    st.Y,
				Right:  st.X + int32(d.Width),
				Bottom: st.Y + int32(d.Height),
			}
			d.RefreshNum, d.RefreshDen = modeRefresh(st.Mode)
		} else {
			w, h := parseSysfsMode(readTrimmed(filepath.Join(base, "modes")))
			if w <= 0 || h <= 0 {
				if !hasEnabled {
					continue
				}
			} else {
				d.Width, d.Height = w, h
				d.Rect = Rect{Right: int32(w), Bottom: int32(h)}
			}
		}
		out = append(out, d)
	}

	assignLinuxPrimary(out)
	return out, nil
}

func parseDRMConnectorDir(name string) (cardPart, connName string, ok bool) {
	cardPart, connName, ok = strings.Cut(name, "-")
	if !ok || connName == "" || !strings.HasPrefix(cardPart, "card") {
		return "", "", false
	}
	num := cardPart[len("card"):]
	if num == "" {
		return "", "", false
	}
	for _, r := range num {
		if r < '0' || r > '9' {
			return "", "", false
		}
	}
	return cardPart, connName, true
}

func formatEDIDID(connName string, info EDIDInfo, block []byte) string {
	var suffix string
	switch {
	case info.SerialText != "":
		suffix = info.SerialText
	case info.Serial != 0:
		suffix = fmt.Sprintf("%08X", info.Serial)
	default:
		suffix = fmt.Sprintf("%08x", crc32.ChecksumIEEE(block))
	}
	return fmt.Sprintf("drm:%s:%s%04X:%s", connName, info.Manufacturer, info.ProductCode, suffix)
}

func readTrimmed(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func parseSysfsMode(modes string) (w, h int) {
	line, _, _ := strings.Cut(modes, "\n")
	line = strings.TrimSpace(line)
	ws, hs, ok := strings.Cut(line, "x")
	if !ok {
		return 0, 0
	}
	w, err := strconv.Atoi(ws)
	if err != nil || w <= 0 {
		return 0, 0
	}
	hs = strings.TrimRight(hs, "ipIP")
	h, err = strconv.Atoi(hs)
	if err != nil || h <= 0 {
		return 0, 0
	}
	return w, h
}

// assignLinuxPrimary marks the primary desktop output. Under X11, extended
// CRTCs occupy distinct Rect coordinates and the output at (0,0) is primary.
// Under Wayland compositors, DRM CRTCs all report (0,0), so prefer an internal
// panel (eDP/LVDS/DSI) or the first non-capture-card display.
func assignLinuxPrimary(out []Display) {
	if len(out) == 0 {
		return
	}
	hasOffset := false
	for _, d := range out {
		if d.Rect.Left != 0 || d.Rect.Top != 0 {
			hasOffset = true
			break
		}
	}
	if hasOffset {
		for i := range out {
			if out[i].Rect.Left == 0 && out[i].Rect.Top == 0 {
				out[i].Primary = true
			}
		}
		return
	}
	for i := range out {
		if isInternalConnector(out[i].Name) && !out[i].LikelyCard {
			out[i].Primary = true
			return
		}
	}
	for i := range out {
		if !out[i].LikelyCard {
			out[i].Primary = true
			return
		}
	}
}

func isInternalConnector(name string) bool {
	return strings.HasPrefix(name, "eDP-") || strings.HasPrefix(name, "LVDS-") || strings.HasPrefix(name, "DSI-")
}
