//go:build linux

package alsa

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ListDevices enumerates ALSA hardware PCM devices from /proc/asound.
func ListDevices() ([]DeviceInfo, error) {
	cardsBytes, err := os.ReadFile("/proc/asound/cards")
	if err != nil {
		return nil, fmt.Errorf("read /proc/asound/cards: %w", err)
	}
	pcmBytes, err := os.ReadFile("/proc/asound/pcm")
	if err != nil {
		return nil, fmt.Errorf("read /proc/asound/pcm: %w", err)
	}

	eldByCard := make(map[int][]string)
	cards := ParseProcAsound(string(cardsBytes), string(pcmBytes), nil)
	seenCards := make(map[int]bool)
	for _, d := range cards {
		if seenCards[d.Card] {
			continue
		}
		seenCards[d.Card] = true
		matches, _ := filepath.Glob(fmt.Sprintf("/proc/asound/card%d/eld#*", d.Card))
		sort.Strings(matches)
		for _, m := range matches {
			eldByCard[d.Card] = append(eldByCard[d.Card], parseELDMonitorName(m))
		}
	}
	return ParseProcAsound(string(cardsBytes), string(pcmBytes), eldByCard), nil
}

func parseELDMonitorName(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var present bool
	var mon string
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		line := sc.Text()
		if k, v, ok := strings.Cut(line, "\t"); ok {
			switch strings.TrimSpace(k) {
			case "monitor_present":
				present = strings.TrimSpace(v) == "1"
			case "monitor_name":
				mon = strings.TrimSpace(v)
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			switch fields[0] {
			case "monitor_present":
				present = fields[1] == "1"
			case "monitor_name":
				mon = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "monitor_name"))
			}
		}
	}
	if !present {
		return ""
	}
	return mon
}

// Inputs returns ALSA capture device names.
func Inputs() ([]string, error) {
	devs, err := ListDevices()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range devs {
		if d.Capture {
			out = append(out, d.DisplayName())
		}
	}
	return out, nil
}

// Outputs returns ALSA playback device names.
func Outputs() ([]string, error) {
	devs, err := ListDevices()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range devs {
		if d.Playback {
			out = append(out, d.DisplayName())
		}
	}
	return out, nil
}

// Start opens cfg.Device for ALSA capture and calls onPacket for every period
// on the capture goroutine.
func Start(cfg Config, now func() int64, freq int64, onPacket func(Packet)) (*Capture, error) {
	drv, err := newPuregoDriver()
	if err != nil {
		return nil, err
	}
	return startCaptureWith(drv, cfg, now, freq, onPacket)
}

// OpenPlayback opens cfg.Device for ALSA playback.
func OpenPlayback(cfg PlaybackConfig) (*Playback, error) {
	drv, err := newPuregoDriver()
	if err != nil {
		return nil, err
	}
	return openPlaybackWith(drv, cfg)
}
