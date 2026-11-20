package ffmpeg

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Mode is one capture mode a device advertises.
type Mode struct {
	Format         string // pixel format or codec (mjpeg)
	MinW, MinH     int
	MaxW, MaxH     int
	MinFPS, MaxFPS float64
}

var (
	reDevice = regexp.MustCompile(`"([^"]+)" \(video\)`)
	reMode   = regexp.MustCompile(`(pixel_format|vcodec)=(\S+)\s+min s=(\d+)x(\d+) fps=([\d.]+) max s=(\d+)x(\d+) fps=([\d.]+)`)
)

// ParseDevices extracts DirectShow video device names from
// `ffmpeg -list_devices true -f dshow -i dummy` output.
func ParseDevices(out string) []string {
	var devs []string
	seen := map[string]bool{}
	for _, m := range reDevice.FindAllStringSubmatch(out, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			devs = append(devs, m[1])
		}
	}
	return devs
}

// ParseModes extracts modes from `ffmpeg -list_options true -f dshow -i
// video=NAME` output. Duplicate lines (one per colour space) are merged.
func ParseModes(out string) []Mode {
	var modes []Mode
	seen := map[Mode]bool{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		m := reMode.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		atoi := func(s string) int { v, _ := strconv.Atoi(s); return v }
		atof := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
		md := Mode{Format: m[2], MinW: atoi(m[3]), MinH: atoi(m[4]), MinFPS: atof(m[5]),
			MaxW: atoi(m[6]), MaxH: atoi(m[7]), MaxFPS: atof(m[8])}
		if !seen[md] {
			seen[md] = true
			modes = append(modes, md)
		}
	}
	return modes
}

// preference ranks raw formats; compressed ones come last.
var preference = []string{"nv12", "yuyv422", "yuv420p", "uyvy422", "rgb24", "bgr24", "bgr0"}

func rank(f string) int {
	for i, p := range preference {
		if f == p {
			return i
		}
	}
	if IsCompressed(f) {
		return 1000
	}
	return 100
}

// PickFormat chooses the best format that supports w x h at fps: raw formats
// first (nv12 > yuyv422 > ...), compressed (mjpeg) only as a last resort.
func PickFormat(modes []Mode, w, h int, fps float64) (string, error) {
	best, bestRank := "", 1<<30
	for _, m := range modes {
		if w < m.MinW || w > m.MaxW || h < m.MinH || h > m.MaxH {
			continue
		}
		if fps > 0 && m.MaxFPS+0.01 < fps {
			continue
		}
		if r := rank(m.Format); r < bestRank {
			best, bestRank = m.Format, r
		}
	}
	if best == "" {
		return "", fmt.Errorf("no mode supports %dx%d at %.3f fps", w, h, fps)
	}
	return best, nil
}

func run(ctx context.Context, bin string, args ...string) string {
	if bin == "" {
		bin = "ffmpeg"
	}
	// These listings always "fail" (there is no real input); the text is on
	// stderr either way.
	out, _ := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	return string(out)
}

// ListDevices returns the DirectShow video capture devices.
func ListDevices(ctx context.Context, bin string) ([]string, error) {
	out := run(ctx, bin, "-hide_banner", "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	devs := ParseDevices(out)
	if len(devs) == 0 && !strings.Contains(out, "dshow") {
		return nil, fmt.Errorf("ffmpeg did not list DirectShow devices (is ffmpeg installed and on PATH?): %s", strings.TrimSpace(out))
	}
	return devs, nil
}

// ListModes returns the modes a device advertises.
func ListModes(ctx context.Context, bin, device string) ([]Mode, error) {
	out := run(ctx, bin, "-hide_banner", "-list_options", "true", "-f", "dshow", "-i", "video="+device)
	modes := ParseModes(out)
	if len(modes) == 0 {
		return nil, fmt.Errorf("no modes listed for %q: %s", device, strings.TrimSpace(out))
	}
	return modes, nil
}
