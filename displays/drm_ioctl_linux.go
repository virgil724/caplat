//go:build linux

package displays

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

type linuxDRMCard struct {
	fd int
}

func openDRMDevice(path string) (drmCard, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return &linuxDRMCard{fd: fd}, nil
}

func (c *linuxDRMCard) Close() error {
	if c.fd >= 0 {
		fd := c.fd
		c.fd = -1
		return unix.Close(fd)
	}
	return nil
}

func (c *linuxDRMCard) ioctl(req uintptr, ptr unsafe.Pointer) error {
	for {
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(c.fd), req, uintptr(ptr))
		if errno == unix.EINTR {
			continue
		}
		if errno != 0 {
			return errno
		}
		return nil
	}
}

func (c *linuxDRMCard) ActiveConnectors() (map[string]drmConnectorState, error) {
	var res drmModeCardRes
	if err := c.ioctl(drmIoctlModeGetResources, unsafe.Pointer(&res)); err != nil {
		return nil, err
	}
	out := make(map[string]drmConnectorState)
	if res.CountConnectors == 0 {
		return out, nil
	}
	connIDs := make([]uint32, res.CountConnectors)
	res = drmModeCardRes{
		ConnectorIDPtr:  uint64(uintptr(unsafe.Pointer(&connIDs[0]))),
		CountConnectors: uint32(len(connIDs)),
	}
	if err := c.ioctl(drmIoctlModeGetResources, unsafe.Pointer(&res)); err != nil {
		return nil, err
	}
	runtime.KeepAlive(connIDs)

	n := min(int(res.CountConnectors), len(connIDs))
	var dummyMode drmModeModeInfo
	for _, cid := range connIDs[:n] {
		// Supply a 1-element mode buffer so the kernel skips expensive DDC
		// fill_modes probes (same pattern as drmModeGetConnectorCurrent).
		conn := drmModeGetConnector{
			ConnectorID: cid,
			ModesPtr:    uint64(uintptr(unsafe.Pointer(&dummyMode))),
			CountModes:  1,
		}
		if err := c.ioctl(drmIoctlModeGetConnector, unsafe.Pointer(&conn)); err != nil {
			continue
		}
		runtime.KeepAlive(&dummyMode)
		if conn.Connection != drmModeConnected || conn.EncoderID == 0 {
			continue
		}
		enc := drmModeGetEncoder{EncoderID: conn.EncoderID}
		if err := c.ioctl(drmIoctlModeGetEncoder, unsafe.Pointer(&enc)); err != nil {
			continue
		}
		if enc.CRTCID == 0 {
			continue
		}
		crtc := drmModeCRTC{CRTCID: enc.CRTCID}
		if err := c.ioctl(drmIoctlModeGetCRTC, unsafe.Pointer(&crtc)); err != nil {
			continue
		}
		if crtc.ModeValid == 0 || crtc.Mode.HDisplay == 0 || crtc.Mode.VDisplay == 0 {
			continue
		}
		name := drmConnectorName(conn.ConnectorType, conn.ConnectorTypeID)
		out[name] = drmConnectorState{
			X:         int32(crtc.X),
			Y:         int32(crtc.Y),
			Mode:      crtc.Mode,
			ModeValid: true,
		}
	}
	return out, nil
}
