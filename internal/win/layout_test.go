//go:build windows

package win

import (
	"testing"
	"unsafe"
)

// Struct layouts must match the C definitions exactly, on amd64 and arm64.
func TestLayouts(t *testing.T) {
	var fs FrameStatistics
	cases := []struct {
		name      string
		got, want uintptr
	}{
		{"FrameStatistics", unsafe.Sizeof(fs), 32},
		{"FrameStatistics.SyncQPCTime", unsafe.Offsetof(fs.SyncQPCTime), 16},
		{"SwapChainDesc1", unsafe.Sizeof(SwapChainDesc1{}), 48},
		{"OutputDesc", unsafe.Sizeof(OutputDesc{}), 96},
		{"Box", unsafe.Sizeof(Box{}), 24},
		{"PathSourceInfo", unsafe.Sizeof(PathSourceInfo{}), 20},
		{"PathTargetInfo", unsafe.Sizeof(PathTargetInfo{}), 48},
		{"PathInfo", unsafe.Sizeof(PathInfo{}), 72},
		{"modeInfo", unsafe.Sizeof(modeInfo{}), 64},
		{"sourceDeviceName", unsafe.Sizeof(sourceDeviceName{}), 84},
		{"targetDeviceName", unsafe.Sizeof(targetDeviceName{}), 420},
		{"Msg", unsafe.Sizeof(Msg{}), 48},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: size/offset %d, want %d", c.name, c.got, c.want)
		}
	}
}
