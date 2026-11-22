package displays

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// EDIDInfo contains identification fields decoded from a raw EDID block.
type EDIDInfo struct {
	Manufacturer string // 3-letter PNP ID, e.g. "AVT"
	ProductCode  uint16
	Serial       uint32
	SerialText   string
	MonitorName  string
}

// FriendlyName returns the monitor's human-readable name from the EDID, falling
// back to "<Manufacturer> <ProductCode>" when no 0xFC descriptor is present.
func (e EDIDInfo) FriendlyName() string {
	if e.MonitorName != "" {
		return e.MonitorName
	}
	if e.Manufacturer != "" {
		return fmt.Sprintf("%s %04X", e.Manufacturer, e.ProductCode)
	}
	return ""
}

var edidHeader = [8]byte{0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x00}

// ParseEDID parses the base 128-byte EDID block from raw.
func ParseEDID(raw []byte) (EDIDInfo, error) {
	if len(raw) < 128 {
		return EDIDInfo{}, fmt.Errorf("edid: length %d < 128 bytes", len(raw))
	}
	for i, b := range edidHeader {
		if raw[i] != b {
			return EDIDInfo{}, errors.New("edid: invalid header")
		}
	}
	var sum uint32
	for _, b := range raw[:128] {
		sum += uint32(b)
	}
	if sum&0xFF != 0 {
		return EDIDInfo{}, fmt.Errorf("edid: bad checksum (sum=0x%02x)", sum&0xFF)
	}

	var info EDIDInfo
	mfgWord := binary.BigEndian.Uint16(raw[8:10])
	c1 := (mfgWord >> 10) & 0x1F
	c2 := (mfgWord >> 5) & 0x1F
	c3 := mfgWord & 0x1F
	if c1 >= 1 && c1 <= 26 && c2 >= 1 && c2 <= 26 && c3 >= 1 && c3 <= 26 {
		info.Manufacturer = string([]byte{'A' + byte(c1-1), 'A' + byte(c2-1), 'A' + byte(c3-1)})
	}
	info.ProductCode = binary.LittleEndian.Uint16(raw[10:12])
	info.Serial = binary.LittleEndian.Uint32(raw[12:16])

	// Four 18-byte descriptor blocks at offsets 54, 72, 90, 108.
	for off := 54; off <= 108; off += 18 {
		blk := raw[off : off+18]
		if blk[0] != 0 || blk[1] != 0 || blk[2] != 0 {
			continue
		}
		switch blk[3] {
		case 0xFC:
			if info.MonitorName == "" {
				info.MonitorName = cleanEDIDText(blk[5:18])
			}
		case 0xFF:
			if info.SerialText == "" {
				info.SerialText = cleanEDIDText(blk[5:18])
			}
		}
	}
	return info, nil
}

func cleanEDIDText(b []byte) string {
	for i, c := range b {
		if c == 0x0A || c == 0x00 {
			b = b[:i]
			break
		}
	}
	return strings.TrimFunc(string(b), func(r rune) bool {
		return r <= ' ' || r >= 0x7F
	})
}
