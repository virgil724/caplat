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
	"MJPG": "mjpeg",
	"H264": "h264",
}

// mergeModes returns primary plus the entries of extra whose format does not
// appear in primary (e.g. mjpeg, which only the ffmpeg backend can decode).
func mergeModes(primary, extra []Mode) []Mode {
	have := map[string]bool{}
	for _, m := range primary {
		have[m.Format] = true
	}
	out := primary
	for _, m := range extra {
		if !have[m.Format] {
			out = append(out, m)
		}
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
