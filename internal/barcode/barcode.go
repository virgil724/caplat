// Package barcode encodes a frame number as a row of black and white cells
// along the top edge of the picture, and decodes it from a captured luma
// plane.
//
// Layout, 32 square cells across the full width, left to right:
//
//	[1][0] [24-bit frame number, MSB first] [CRC-4 of the number] [1][0]
//
// The cell size is a fraction of the width, so the code survives any scaling
// the capture card applies. 24 bits wrap after about 77 hours at 60 Hz (the
// matcher unwraps them anyway); the CRC rejects frames corrupted by scaling
// noise instead of reading them as another frame.
package barcode

// Cells is the number of cells in the row.
const Cells = 32

// Bits is the width of the frame number.
const Bits = 24

// Mask keeps the encoded part of a frame number.
const Mask = 1<<Bits - 1

// NextFrame returns the frame number after f, skipping numbers whose code
// would be 0: code 0 is reserved for warm-up frames that are not measured.
func NextFrame(f uint64) uint64 {
	f++
	if f&Mask == 0 {
		f++
	}
	return f
}

// crc4 is CRC-4/ITU (polynomial x^4 + x + 1) over the 24-bit value, MSB first.
func crc4(v uint32) uint8 {
	var crc uint8
	for i := Bits - 1; i >= 0; i-- {
		bit := uint8(v>>uint(i)) & 1
		top := (crc >> 3) & 1
		crc = (crc << 1) & 0xF
		if top^bit == 1 {
			crc ^= 0x3
		}
	}
	return crc
}

// Encode returns the cell colors (true = white) for frame number n.
func Encode(n uint32) [Cells]bool {
	var c [Cells]bool
	n &= Mask
	c[0], c[1] = true, false
	for i := 0; i < Bits; i++ {
		c[2+i] = (n>>uint(Bits-1-i))&1 == 1
	}
	crc := crc4(n)
	for i := 0; i < 4; i++ {
		c[2+Bits+i] = (crc>>uint(3-i))&1 == 1
	}
	c[30], c[31] = true, false
	return c
}

// FromCells checks the sync cells and CRC and returns the frame number.
func FromCells(c [Cells]bool) (uint32, bool) {
	if !c[0] || c[1] || !c[30] || c[31] {
		return 0, false
	}
	var n uint32
	for i := 0; i < Bits; i++ {
		n <<= 1
		if c[2+i] {
			n |= 1
		}
	}
	var crc uint8
	for i := 0; i < 4; i++ {
		crc <<= 1
		if c[2+Bits+i] {
			crc |= 1
		}
	}
	if crc != crc4(n) {
		return 0, false
	}
	return n, true
}

// Geometry maps the barcode, laid out on an output of OutW x OutH, onto a
// captured picture of CapW x CapH (the card scales the whole frame).
type Geometry struct {
	OutW, OutH int
	CapW, CapH int
}

// CellSize is the cell edge in output pixels.
func (g Geometry) CellSize() int { return g.OutW / Cells }

// Decode reads the barcode from an 8-bit luma plane with the given stride.
// Each cell is the mean of a 3x3 grid around its center, thresholded at 128.
func (g Geometry) Decode(y []byte, stride int) (uint32, bool) {
	cw := float64(g.CapW) / Cells
	ch := float64(g.CellSize()) * float64(g.CapH) / float64(g.OutH) // square in output pixels
	if cw < 3 || ch < 3 {
		return 0, false
	}
	dx, dy := int(cw/4), int(ch/4)
	cy := int(ch / 2)
	var cells [Cells]bool
	for i := 0; i < Cells; i++ {
		cx := int(cw*float64(i) + cw/2)
		sum := 0
		for oy := -dy; oy <= dy; oy += max(dy, 1) {
			for ox := -dx; ox <= dx; ox += max(dx, 1) {
				p := (cy+oy)*stride + cx + ox
				if p < 0 || p >= len(y) {
					return 0, false
				}
				sum += int(y[p])
			}
		}
		cells[i] = sum/9 > 128
	}
	return FromCells(cells)
}
