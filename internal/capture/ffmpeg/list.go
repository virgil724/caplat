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
	reDevice       = regexp.MustCompile(`"([^"]+)" \(video\)`)
	reMode         = regexp.MustCompile(`(pixel_format|vcodec)=(\S+)\s+min s=(\d+)x(\d+) fps=([\d.]+) max s=(\d+)x(\d+) fps=([\d.]+)`)
	reV4L2Line     = regexp.MustCompile(`(?:Raw|Compressed)\s*:\s*(\S+)\s+:\s+.*?\s+:\s+(.+)$`)
	reV4L2Discrete = regexp.MustCompile(`\b(\d+)x(\d+)\b`)
	reV4L2Stepwise = regexp.MustCompile(`\{(\d+)-(\d+),\s*\d+\}x\{(\d+)-(\d+),\s*\d+\}`)
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

// ParseV4L2Modes extracts modes from `ffmpeg -hide_banner -f v4l2
// -list_formats all -i /dev/videoN` output.
func ParseV4L2Modes(out string) []Mode {
	var modes []Mode
	seen := map[Mode]bool{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		lm := reV4L2Line.FindStringSubmatch(sc.Text())
		if lm == nil {
			continue
		}
		format := strings.TrimSpace(lm[1])
		if strings.EqualFold(format, "unsupported") {
			continue
		}
		sizeSpec := lm[2]
		add := func(md Mode) {
			if !seen[md] {
				seen[md] = true
				modes = append(modes, md)
			}
		}
		for _, m := range reV4L2Stepwise.FindAllStringSubmatch(sizeSpec, -1) {
			minW, _ := strconv.Atoi(m[1])
			maxW, _ := strconv.Atoi(m[2])
			minH, _ := strconv.Atoi(m[3])
			maxH, _ := strconv.Atoi(m[4])
			if maxW > 0 && maxH > 0 {
				add(Mode{Format: format, MinW: minW, MinH: minH, MaxW: maxW, MaxH: maxH})
			}
		}
		for _, m := range reV4L2Discrete.FindAllStringSubmatch(sizeSpec, -1) {
			w, _ := strconv.Atoi(m[1])
			h, _ := strconv.Atoi(m[2])
			if w > 0 && h > 0 {
				add(Mode{Format: format, MinW: w, MinH: h, MaxW: w, MaxH: h})
			}
		}
	}
	return modes
}

// preference ranks raw formats; compressed ones come last.
var preference = []string{"nv12", "yuyv422", "yuv420p", "uyvy422", "p010le", "rgb24", "bgr24", "bgr0"}

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
		if fps > 0 && m.MaxFPS > 0 && m.MaxFPS+0.01 < fps {
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
