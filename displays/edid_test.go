package displays

import (
	"encoding/binary"
	"strings"
	"testing"
)

func buildTestEDID(mfg string, prod uint16, serial uint32, monitorName, serialText string) []byte {
	raw := make([]byte, 128)
	copy(raw[:8], edidHeader[:])
	if len(mfg) == 3 {
		w := (uint16(mfg[0]-'A'+1) << 10) | (uint16(mfg[1]-'A'+1) << 5) | uint16(mfg[2]-'A'+1)
		binary.BigEndian.PutUint16(raw[8:10], w)
	}
	binary.LittleEndian.PutUint16(raw[10:12], prod)
	binary.LittleEndian.PutUint32(raw[12:16], serial)
	raw[18], raw[19] = 1, 4 // EDID 1.4

	writeDesc := func(off int, tag byte, text string) {
		raw[off], raw[off+1], raw[off+2], raw[off+3], raw[off+4] = 0, 0, 0, tag, 0
		dst := raw[off+5 : off+18]
		for i := range dst {
			dst[i] = 0x20
		}
		n := copy(dst, text)
		if n < len(dst) {
			dst[n] = 0x0A
		}
	}
	if monitorName != "" {
		writeDesc(54, 0xFC, monitorName)
	}
	if serialText != "" {
		writeDesc(72, 0xFF, serialText)
	}
	var sum byte
	for i := 0; i < 127; i++ {
		sum += raw[i]
	}
	raw[127] = byte(256 - int(sum))
	return raw
}

func TestParseEDIDFixtures(t *testing.T) {
	cases := []struct {
		name       string
		edid       []byte
		wantMfg    string
		wantProd   uint16
		wantName   string
		wantSerial string
		wantCard   bool
	}{
		{
			name:     "AVT_GC553",
			edid:     buildTestEDID("AVT", 0x0553, 0x12345678, "AVT GC553", ""),
			wantMfg:  "AVT",
			wantProd: 0x0553,
			wantName: "AVT GC553",
			wantCard: true,
		},
		{
			name:       "Elgato_HD60",
			edid:       buildTestEDID("EGT", 0x4001, 42, "Elgato HD60 X", "ELG001"),
			wantMfg:    "EGT",
			wantProd:   0x4001,
			wantName:   "Elgato HD60 X",
			wantSerial: "ELG001",
			wantCard:   true,
		},
		{
			name:     "Standard_Monitor",
			edid:     buildTestEDID("MSI", 0x3CC0, 1001, "MAG274QRF-QD", ""),
			wantMfg:  "MSI",
			wantProd: 0x3CC0,
			wantName: "MAG274QRF-QD",
			wantCard: false,
		},
		{
			name:     "Fallback_No_0xFC",
			edid:     buildTestEDID("DEL", 0xA0B1, 99, "", ""),
			wantMfg:  "DEL",
			wantProd: 0xA0B1,
			wantName: "DEL A0B1",
			wantCard: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := ParseEDID(tc.edid)
			if err != nil {
				t.Fatalf("ParseEDID: %v", err)
			}
			if info.Manufacturer != tc.wantMfg || info.ProductCode != tc.wantProd {
				t.Errorf("got mfg=%q prod=%#04x, want %q %#04x", info.Manufacturer, info.ProductCode, tc.wantMfg, tc.wantProd)
			}
			if info.FriendlyName() != tc.wantName {
				t.Errorf("FriendlyName() = %q, want %q", info.FriendlyName(), tc.wantName)
			}
			if info.SerialText != tc.wantSerial {
				t.Errorf("SerialText = %q, want %q", info.SerialText, tc.wantSerial)
			}
			if LikelyCard(info.FriendlyName()) != tc.wantCard {
				t.Errorf("LikelyCard(%q) = %v, want %v", info.FriendlyName(), LikelyCard(info.FriendlyName()), tc.wantCard)
			}
		})
	}
}

func TestParseEDIDErrors(t *testing.T) {
	if _, err := ParseEDID(make([]byte, 64)); err == nil || !strings.Contains(err.Error(), "length") {
		t.Errorf("truncated EDID: %v", err)
	}
	badHdr := buildTestEDID("AVT", 1, 1, "AVT GC553", "")
	badHdr[0] = 0xAA
	if _, err := ParseEDID(badHdr); err == nil || !strings.Contains(err.Error(), "header") {
		t.Errorf("bad header: %v", err)
	}
	badCRC := buildTestEDID("AVT", 1, 1, "AVT GC553", "")
	badCRC[127] ^= 0x01
	if _, err := ParseEDID(badCRC); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("bad checksum: %v", err)
	}
}
