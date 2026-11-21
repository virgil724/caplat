package capture

// NeedsLumaBuffer reports whether fourcc requires an external w*h scratch
// buffer to extract an 8-bit luma plane for Frame.Y.
func NeedsLumaBuffer(fourcc string) bool {
	switch fourcc {
	case "NV12", "I420", "IYUV", "YV12":
		return false
	default:
		return true
	}
}

// DefaultPitch returns the tightly-packed row pitch in bytes for fourcc at
// width w, or 0 for unknown formats.
func DefaultPitch(fourcc string, w int) int {
	switch fourcc {
	case "NV12", "I420", "IYUV", "YV12":
		return w
	case "YUY2", "UYVY", "P010":
		return w * 2
	case "RGB24":
		return w * 3
	case "RGB32":
		return w * 4
	}
	return 0
}

// RawSize returns the total byte size of one frame of fourcc with row pitch
// and height h, or 0 for unknown formats.
func RawSize(fourcc string, pitch, h int) int {
	switch fourcc {
	case "NV12", "I420", "IYUV", "YV12", "P010":
		return pitch * h * 3 / 2
	case "YUY2", "UYVY", "RGB24", "RGB32":
		return pitch * h
	}
	return 0
}

// ExtractLuma populates an 8-bit top-down luma plane (y, stride) and slices
// raw to the frame's byte range.
//
// For 8-bit 4:2:0 planar formats (NV12, I420, IYUV, YV12) y aliases data
// directly with zero copy. For P010, YUY2, UYVY, RGB24, and RGB32, luma is
// written into scratch (which must have len >= w*h). When bottomUp is true
// (typical for Windows RGB24/RGB32 DIB buffers), rows are flipped into
// top-down order so the barcode at the top of the frame sits at row 0 of y.
func ExtractLuma(fourcc string, data []byte, pitch, w, h int, bottomUp bool, scratch []byte) (y []byte, stride int, raw []byte, rawPitch int, ok bool) {
	if w <= 0 || h <= 0 {
		return nil, 0, nil, 0, false
	}
	minPitch := DefaultPitch(fourcc, w)
	if minPitch == 0 {
		return nil, 0, nil, 0, false
	}
	if pitch <= 0 {
		pitch = minPitch
	}
	if pitch < minPitch {
		return nil, 0, nil, 0, false
	}
	need := RawSize(fourcc, pitch, h)
	if len(data) < pitch*h {
		return nil, 0, nil, 0, false
	}
	raw = data[:min(len(data), need)]
	rawPitch = pitch

	switch fourcc {
	case "NV12", "I420", "IYUV", "YV12":
		return data[:pitch*h], pitch, raw, rawPitch, true

	case "P010":
		// 10-bit 4:2:0 planar, 16-bit LE per sample with luma in bits [15:6].
		// The high byte of each uint16 is the top 8 bits of luma.
		if len(scratch) < w*h {
			return nil, 0, nil, 0, false
		}
		for row := 0; row < h; row++ {
			src := data[row*pitch : row*pitch+w*2]
			dst := scratch[row*w : row*w+w]
			for x := 0; x < w; x++ {
				dst[x] = src[2*x+1]
			}
		}
		return scratch[:w*h], w, raw, rawPitch, true

	case "YUY2":
		if len(scratch) < w*h {
			return nil, 0, nil, 0, false
		}
		for row := 0; row < h; row++ {
			src := data[row*pitch : row*pitch+w*2]
			dst := scratch[row*w : row*w+w]
			for x := 0; x < w; x++ {
				dst[x] = src[2*x]
			}
		}
		return scratch[:w*h], w, raw, rawPitch, true

	case "UYVY":
		if len(scratch) < w*h {
			return nil, 0, nil, 0, false
		}
		for row := 0; row < h; row++ {
			src := data[row*pitch : row*pitch+w*2]
			dst := scratch[row*w : row*w+w]
			for x := 0; x < w; x++ {
				dst[x] = src[2*x+1]
			}
		}
		return scratch[:w*h], w, raw, rawPitch, true

	case "RGB24":
		// BGR 24-bit (B, G, R). BT.709 integer luma: (54*R + 183*G + 19*B) >> 8.
		if len(scratch) < w*h {
			return nil, 0, nil, 0, false
		}
		for row := 0; row < h; row++ {
			srcRow := row
			if bottomUp {
				srcRow = h - 1 - row
			}
			src := data[srcRow*pitch : srcRow*pitch+w*3]
			dst := scratch[row*w : row*w+w]
			for x := 0; x < w; x++ {
				b := int(src[3*x])
				g := int(src[3*x+1])
				r := int(src[3*x+2])
				dst[x] = byte((54*r + 183*g + 19*b) >> 8)
			}
		}
		return scratch[:w*h], w, raw, rawPitch, true

	case "RGB32":
		// BGRX 32-bit (B, G, R, X).
		if len(scratch) < w*h {
			return nil, 0, nil, 0, false
		}
		for row := 0; row < h; row++ {
			srcRow := row
			if bottomUp {
				srcRow = h - 1 - row
			}
			src := data[srcRow*pitch : srcRow*pitch+w*4]
			dst := scratch[row*w : row*w+w]
			for x := 0; x < w; x++ {
				b := int(src[4*x])
				g := int(src[4*x+1])
				r := int(src[4*x+2])
				dst[x] = byte((54*r + 183*g + 19*b) >> 8)
			}
		}
		return scratch[:w*h], w, raw, rawPitch, true
	}
	return nil, 0, nil, 0, false
}
