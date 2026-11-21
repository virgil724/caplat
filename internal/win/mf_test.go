//go:build windows

package win

import "testing"

func TestSubtypeMapping(t *testing.T) {
	mfRGB24 := guid(20, 0x0000, 0x0010, 0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71)
	mfRGB32 := guid(22, 0x0000, 0x0010, 0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71)

	cases := []struct {
		g    GUID
		want string
	}{
		{VideoFormatGUID("NV12"), "NV12"},
		{VideoFormatGUID("P010"), "P010"},
		{VideoFormatGUID("YUY2"), "YUY2"},
		{VideoFormatGUID("UYVY"), "UYVY"},
		{mfRGB24, "RGB24"},
		{mfRGB32, "RGB32"},
	}
	for _, tc := range cases {
		if got := SubtypeName(tc.g); got != tc.want {
			t.Errorf("SubtypeName(%v) = %q, want %q", tc.g, got, tc.want)
		}
	}

	for _, name := range []string{"NV12", "P010", "YUY2", "UYVY", "RGB24", "RGB32"} {
		g := DShowSubtypeGUID(name)
		if got := DShowSubtypeName(g); got != name {
			t.Errorf("DShowSubtypeName(DShowSubtypeGUID(%q)) = %q, want %q", name, got, name)
		}
	}
}
