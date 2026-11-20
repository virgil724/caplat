//go:build windows

package win

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetDisplayConfigBufferSizes = modUser32.NewProc("GetDisplayConfigBufferSizes")
	procQueryDisplayConfig          = modUser32.NewProc("QueryDisplayConfig")
	procDisplayConfigGetDeviceInfo  = modUser32.NewProc("DisplayConfigGetDeviceInfo")
)

const (
	qdcOnlyActivePaths = 0x2
	dcGetSourceName    = 1
	dcGetTargetName    = 2
)

// LUID identifies an adapter.
type LUID struct {
	LowPart  uint32
	HighPart int32
}

// Rational is DISPLAYCONFIG_RATIONAL.
type Rational struct{ Numerator, Denominator uint32 }

// PathSourceInfo is DISPLAYCONFIG_PATH_SOURCE_INFO (20 bytes).
type PathSourceInfo struct {
	AdapterID   LUID
	ID          uint32
	ModeInfoIdx uint32
	StatusFlags uint32
}

// PathTargetInfo is DISPLAYCONFIG_PATH_TARGET_INFO (48 bytes).
type PathTargetInfo struct {
	AdapterID        LUID
	ID               uint32
	ModeInfoIdx      uint32
	OutputTechnology uint32
	Rotation         uint32
	Scaling          uint32
	RefreshRate      Rational
	ScanLineOrdering uint32
	TargetAvailable  int32
	StatusFlags      uint32
}

// PathInfo is DISPLAYCONFIG_PATH_INFO (72 bytes).
type PathInfo struct {
	Source PathSourceInfo
	Target PathTargetInfo
	Flags  uint32
}

// modeInfo is DISPLAYCONFIG_MODE_INFO (64 bytes); its contents are unused.
type modeInfo [64]byte

type deviceInfoHeader struct {
	Type      uint32
	Size      uint32
	AdapterID LUID
	ID        uint32
}

type sourceDeviceName struct {
	Header         deviceInfoHeader
	ViewGdiDevName [32]uint16
}

type targetDeviceName struct {
	Header                    deviceInfoHeader
	Flags                     uint32
	OutputTechnology          uint32
	EdidManufactureID         uint16
	EdidProductCodeID         uint16
	ConnectorInstance         uint32
	MonitorFriendlyDeviceName [64]uint16
	MonitorDevicePath         [128]uint16
}

// DisplayPath is one active display path.
type DisplayPath struct {
	GDIName      string   // \\.\DISPLAYn
	FriendlyName string   // from EDID, e.g. "AVT GC553"
	Refresh      Rational // target refresh rate, e.g. 60000/1001
}

// ActiveDisplayPaths lists the active display paths.
func ActiveDisplayPaths() ([]DisplayPath, error) {
	var nPath, nMode uint32
	if r, _, _ := procGetDisplayConfigBufferSizes.Call(qdcOnlyActivePaths,
		uintptr(unsafe.Pointer(&nPath)), uintptr(unsafe.Pointer(&nMode))); r != 0 {
		return nil, windows.Errno(r)
	}
	if nPath == 0 {
		return nil, nil
	}
	paths := make([]PathInfo, nPath)
	modes := make([]modeInfo, max(nMode, 1))
	if r, _, _ := procQueryDisplayConfig.Call(qdcOnlyActivePaths,
		uintptr(unsafe.Pointer(&nPath)), uintptr(unsafe.Pointer(&paths[0])),
		uintptr(unsafe.Pointer(&nMode)), uintptr(unsafe.Pointer(&modes[0])), 0); r != 0 {
		return nil, windows.Errno(r)
	}
	var out []DisplayPath
	for _, p := range paths[:nPath] {
		var src sourceDeviceName
		src.Header = deviceInfoHeader{Type: dcGetSourceName, Size: uint32(unsafe.Sizeof(src)), AdapterID: p.Source.AdapterID, ID: p.Source.ID}
		if r, _, _ := procDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(&src))); r != 0 {
			continue
		}
		var tgt targetDeviceName
		tgt.Header = deviceInfoHeader{Type: dcGetTargetName, Size: uint32(unsafe.Sizeof(tgt)), AdapterID: p.Target.AdapterID, ID: p.Target.ID}
		procDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(&tgt)))
		out = append(out, DisplayPath{
			GDIName:      windows.UTF16ToString(src.ViewGdiDevName[:]),
			FriendlyName: windows.UTF16ToString(tgt.MonitorFriendlyDeviceName[:]),
			Refresh:      p.Target.RefreshRate,
		})
	}
	return out, nil
}
