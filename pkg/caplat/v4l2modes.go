package caplat

import (
	"strings"

	"github.com/virgil724/caplat/capture/v4l2"
)

// v4l2FormatNames maps V4L2 FourCCs to the ffmpeg pixel format / codec
// names used by Mode.Format (and ranked by ffmpeg.PickFormat).
var v4l2FormatNames = map[string]string{
	"NV12": "nv12",
	"NV21": "nv21",
	"YUYV": "yuyv422",
	"UYVY": "uyvy422",
	"YU12": "yuv420p",
	"YV12": "yuv420p",
	"P010": "p010le",
	"RGB3": "rgb24",
	"BGR3": "bgr24",
	"XR24": "bgr0",
	"BGR4": "bgr0",
	"MJPG": "mjpeg",
	"H264": "h264",
	"HEVC": "hevc",
}

// mergeModes returns primary plus the entries of extra whose format does not
// appear in primary (e.g. mjpeg, which only the ffmpeg backend can decode).
// "Unsupported" rows are dropped, and extra entries without a frame rate inherit
// their rate from matching entries in rateHints.
func mergeModes(primary, extra, rateHints []Mode) []Mode {
	type rateKey struct {
		format     string
		minW, minH int
		maxW, maxH int
	}
	type rateRange struct {
		minFPS, maxFPS float64
	}
	rates := map[rateKey]rateRange{}
	for _, m := range rateHints {
		if m.MaxFPS <= 0 {
			continue
		}
		k := rateKey{m.Format, m.MinW, m.MinH, m.MaxW, m.MaxH}
		cur, ok := rates[k]
		if !ok {
			rates[k] = rateRange{minFPS: m.MinFPS, maxFPS: m.MaxFPS}
			continue
		}
		if m.MaxFPS > cur.maxFPS {
			cur.maxFPS = m.MaxFPS
		}
		if m.MinFPS > 0 && (cur.minFPS <= 0 || m.MinFPS < cur.minFPS) {
			cur.minFPS = m.MinFPS
		}
		rates[k] = cur
	}

	have := map[string]bool{}
	var out []Mode
	for _, m := range primary {
		if strings.EqualFold(m.Format, "unsupported") {
			continue
		}
		have[m.Format] = true
		out = append(out, m)
	}
	for _, m := range extra {
		if strings.EqualFold(m.Format, "unsupported") || have[m.Format] {
			continue
		}
		if m.MaxFPS <= 0 {
			if r, ok := rates[rateKey{m.Format, m.MinW, m.MinH, m.MaxW, m.MaxH}]; ok && r.maxFPS > 0 {
				m.MinFPS = r.minFPS
				m.MaxFPS = r.maxFPS
			}
		}
		out = append(out, m)
	}
	return out
}

// modesFromV4L2 converts V4L2 discrete modes into Mode entries (one size and
// frame rate each), keeping only the highest frame rate per format and size.
func modesFromV4L2(ms []v4l2.Mode) []Mode {
	type key struct {
		f    string
		w, h int
	}
	idx := map[key]int{}
	var out []Mode
	for _, m := range ms {
		f, ok := v4l2FormatNames[strings.ToUpper(m.FourCC)]
		if !ok {
			f = strings.ToLower(m.FourCC)
		}
		k := key{f, m.Width, m.Height}
		if i, ok := idx[k]; ok {
			if m.FPS > out[i].MaxFPS {
				out[i].MaxFPS = m.FPS
			}
			if m.FPS < out[i].MinFPS {
				out[i].MinFPS = m.FPS
			}
			continue
		}
		idx[k] = len(out)
		out = append(out, Mode{Format: f, MinW: m.Width, MinH: m.Height, MaxW: m.Width, MaxH: m.Height, MinFPS: m.FPS, MaxFPS: m.FPS})
	}
	return out
}
