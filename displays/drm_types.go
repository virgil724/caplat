package displays

import "fmt"

// DRM mode ioctl request numbers for Linux LP64 (amd64 / arm64).
const (
	drmIoctlModeGetResources = 0xC04064A0
	drmIoctlModeGetCRTC      = 0xC06864A1
	drmIoctlModeGetEncoder   = 0xC01464A6
	drmIoctlModeGetConnector = 0xC05064A7

	drmModeConnected = 1
	drmModeInterlace = 0x10
	drmModeDblScan   = 0x20
)

type drmModeCardRes struct {
	FBIDPtr         uint64
	CRTCIDPtr       uint64
	ConnectorIDPtr  uint64
	EncoderIDPtr    uint64
	CountFBs        uint32
	CountCRTCs      uint32
	CountConnectors uint32
	CountEncoders   uint32
	MinWidth        uint32
	MaxWidth        uint32
	MinHeight       uint32
	MaxHeight       uint32
}

type drmModeGetConnector struct {
	EncodersPtr     uint64
	ModesPtr        uint64
	PropsPtr        uint64
	PropValuesPtr   uint64
	CountModes      uint32
	CountProps      uint32
	CountEncoders   uint32
	EncoderID       uint32
	ConnectorID     uint32
	ConnectorType   uint32
	ConnectorTypeID uint32
	Connection      uint32
	MMWidth         uint32
	MMHeight        uint32
	Subpixel        uint32
	Pad             uint32
}

type drmModeGetEncoder struct {
	EncoderID      uint32
	EncoderType    uint32
	CRTCID         uint32
	PossibleCRTCs  uint32
	PossibleClones uint32
}

type drmModeModeInfo struct {
	Clock      uint32
	HDisplay   uint16
	HSyncStart uint16
	HSyncEnd   uint16
	HTotal     uint16
	HSkew      uint16
	VDisplay   uint16
	VSyncStart uint16
	VSyncEnd   uint16
	VTotal     uint16
	VScan      uint16
	VRefresh   uint32
	Flags      uint32
	Type       uint32
	Name       [32]byte
}

type drmModeCRTC struct {
	SetConnectorsPtr uint64
	CountConnectors  uint32
	CRTCID           uint32
	FBID             uint32
	X                uint32
	Y                uint32
	GammaSize        uint32
	ModeValid        uint32
	Mode             drmModeModeInfo
}

var drmConnectorTypes = map[uint32]string{
	0:  "Unknown",
	1:  "VGA",
	2:  "DVI-I",
	3:  "DVI-D",
	4:  "DVI-A",
	5:  "Composite",
	6:  "SVIDEO",
	7:  "LVDS",
	8:  "Component",
	9:  "DIN",
	10: "DP",
	11: "HDMI-A",
	12: "HDMI-B",
	13: "TV",
	14: "eDP",
	15: "Virtual",
	16: "DSI",
	17: "DPI",
	18: "Writeback",
	19: "SPI",
	20: "USB",
}

func drmConnectorName(connType, typeID uint32) string {
	prefix, ok := drmConnectorTypes[connType]
	if !ok {
		prefix = "Unknown"
	}
	return fmt.Sprintf("%s-%d", prefix, typeID)
}
