package capture_test

import (
	"testing"

	"github.com/virgil724/caplat/capture"
	"github.com/virgil724/caplat/internal/barcode"
)

func TestExtractLumaBarcodeDecode(t *testing.T) {
	const (
		w, h      = 320, 180
		wantFrame = uint32(0x123456)
	)
	geo := barcode.Geometry{OutW: w, OutH: h, CapW: w, CapH: h}
	cells := barcode.Encode(wantFrame)
	cw := w / barcode.Cells // 10 px per cell
	ch := geo.CellSize()    // 10 px tall

	// pixelVal returns 255 for white barcode cells, 0 for black cells or below barcode.
	pixelVal := func(x, y int) byte {
		if y < ch && cells[x/cw] {
			return 255
		}
		return 0
	}

	cases := []struct {
		name     string
		fourcc   string
		padBytes int
		bottomUp bool
		build    func(pitch int) []byte
	}{
		{
			name:   "NV12_padded",
			fourcc: "NV12", padBytes: 16,
			build: func(pitch int) []byte {
				buf := make([]byte, pitch*h*3/2)
				for y := 0; y < h; y++ {
					for x := 0; x < w; x++ {
						buf[y*pitch+x] = pixelVal(x, y)
					}
				}
				return buf
			},
		},
		{
			name:   "P010_padded",
			fourcc: "P010", padBytes: 32,
			build: func(pitch int) []byte {
				buf := make([]byte, pitch*h*3/2)
				for y := 0; y < h; y++ {
					for x := 0; x < w; x++ {
						v := pixelVal(x, y)
						// 10-bit studio-like value in upper 10 bits of uint16 LE:
						// white -> 0xFFC0, black -> 0x0000
						if v > 128 {
							buf[y*pitch+2*x] = 0xC0
							buf[y*pitch+2*x+1] = 0xFF
						}
					}
				}
				return buf
			},
		},
		{
			name:   "YUY2",
			fourcc: "YUY2", padBytes: 16,
			build: func(pitch int) []byte {
				buf := make([]byte, pitch*h)
				for y := 0; y < h; y++ {
					for x := 0; x < w; x++ {
						buf[y*pitch+2*x] = pixelVal(x, y)
						buf[y*pitch+2*x+1] = 128
					}
				}
				return buf
			},
		},
		{
			name:   "UYVY",
			fourcc: "UYVY", padBytes: 16,
			build: func(pitch int) []byte {
				buf := make([]byte, pitch*h)
				for y := 0; y < h; y++ {
					for x := 0; x < w; x++ {
						buf[y*pitch+2*x] = 128
						buf[y*pitch+2*x+1] = pixelVal(x, y)
					}
				}
				return buf
			},
		},
		{
			name:   "RGB24_topdown",
			fourcc: "RGB24", padBytes: 4, bottomUp: false,
			build: func(pitch int) []byte {
				buf := make([]byte, pitch*h)
				for y := 0; y < h; y++ {
					for x := 0; x < w; x++ {
						v := pixelVal(x, y)
						buf[y*pitch+3*x] = v
						buf[y*pitch+3*x+1] = v
						buf[y*pitch+3*x+2] = v
					}
				}
				return buf
			},
		},
		{
			name:   "RGB24_bottomup",
			fourcc: "RGB24", padBytes: 4, bottomUp: true,
			build: func(pitch int) []byte {
				buf := make([]byte, pitch*h)
				for y := 0; y < h; y++ {
					memRow := h - 1 - y
					for x := 0; x < w; x++ {
						v := pixelVal(x, y)
						buf[memRow*pitch+3*x] = v
						buf[memRow*pitch+3*x+1] = v
						buf[memRow*pitch+3*x+2] = v
					}
				}
				return buf
			},
		},
		{
			name:   "RGB32_topdown",
			fourcc: "RGB32", padBytes: 16, bottomUp: false,
			build: func(pitch int) []byte {
				buf := make([]byte, pitch*h)
				for y := 0; y < h; y++ {
					for x := 0; x < w; x++ {
						v := pixelVal(x, y)
						buf[y*pitch+4*x] = v
						buf[y*pitch+4*x+1] = v
						buf[y*pitch+4*x+2] = v
						buf[y*pitch+4*x+3] = 255
					}
				}
				return buf
			},
		},
		{
			name:   "RGB32_bottomup",
			fourcc: "RGB32", padBytes: 16, bottomUp: true,
			build: func(pitch int) []byte {
				buf := make([]byte, pitch*h)
				for y := 0; y < h; y++ {
					memRow := h - 1 - y
					for x := 0; x < w; x++ {
						v := pixelVal(x, y)
						buf[memRow*pitch+4*x] = v
						buf[memRow*pitch+4*x+1] = v
						buf[memRow*pitch+4*x+2] = v
						buf[memRow*pitch+4*x+3] = 255
					}
				}
				return buf
			},
		},
	}

	scratch := make([]byte, w*h)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pitch := capture.DefaultPitch(tc.fourcc, w) + tc.padBytes
			data := tc.build(pitch)
			y, stride, raw, rawPitch, ok := capture.ExtractLuma(tc.fourcc, data, pitch, w, h, tc.bottomUp, scratch)
			if !ok {
				t.Fatalf("ExtractLuma(%s) failed", tc.fourcc)
			}
			if rawPitch != pitch || len(raw) != capture.RawSize(tc.fourcc, pitch, h) {
				t.Fatalf("rawPitch=%d len(raw)=%d, want pitch=%d len=%d", rawPitch, len(raw), pitch, capture.RawSize(tc.fourcc, pitch, h))
			}
			got, decOK := geo.Decode(y, stride)
			if !decOK || got != wantFrame {
				t.Fatalf("Decode = (%#x, %v), want (%#x, true)", got, decOK, wantFrame)
			}
		})
	}
}
