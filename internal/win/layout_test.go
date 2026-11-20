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
		{"AMMediaType", unsafe.Sizeof(AMMediaType{}), 88},
		{"AMMediaType.FormatType", unsafe.Offsetof(AMMediaType{}.FormatType), 44},
		{"AMMediaType.Unk", unsafe.Offsetof(AMMediaType{}.Unk), 64},
		{"AMMediaType.PbFormat", unsafe.Offsetof(AMMediaType{}.PbFormat), 80},
		{"VideoStreamConfigCaps", unsafe.Sizeof(VideoStreamConfigCaps{}), 128},
		{"VideoStreamConfigCaps.MinFrameInterval", unsafe.Offsetof(VideoStreamConfigCaps{}.MinFrameInterval), 104},
		{"variant", unsafe.Sizeof(variant{}), 24},
		{"variant.BSTR", unsafe.Offsetof(variant{}.BSTR), 8},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: size/offset %d, want %d", c.name, c.got, c.want)
		}
	}
}
