//go:build linux

package v4l2

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/virgil724/caplat/capture"
)

type linuxSystem struct{}

func (linuxSystem) Glob(pattern string) ([]string, error) {
	return filepath.Glob(pattern)
}

func (linuxSystem) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (linuxSystem) Open(path string, nonblock bool) (v4l2Device, error) {
	flags := unix.O_RDWR | unix.O_CLOEXEC
	if nonblock {
		flags |= unix.O_NONBLOCK
	}
	fd, err := unix.Open(path, flags, 0)
	if err != nil {
		return nil, err
	}
	wakeFD := -1
	if nonblock {
		wakeFD, err = unix.Eventfd(0, unix.EFD_NONBLOCK|unix.EFD_CLOEXEC)
		if err != nil {
			_ = unix.Close(fd)
			return nil, err
		}
	}
	return &linuxDevice{fd: fd, wakeFD: wakeFD}, nil
}

type linuxDevice struct {
	fd     int
	wakeFD int
	mu     sync.Mutex
	closed bool
}

func (d *linuxDevice) Ioctl(req uintptr, ptr unsafe.Pointer) error {
	for {
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(d.fd), req, uintptr(ptr))
		if errno == unix.EINTR {
			continue
		}
		if errno != 0 {
			return errno
		}
		return nil
	}
}

func (d *linuxDevice) Mmap(offset int64, length int) ([]byte, error) {
	return unix.Mmap(d.fd, offset, length, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
}

func (d *linuxDevice) Munmap(data []byte) error {
	return unix.Munmap(data)
}

func (d *linuxDevice) Poll(timeoutMS int) (frameReady, woke bool, err error) {
	fds := []unix.PollFd{
		{Fd: int32(d.fd), Events: unix.POLLIN},
	}
	if d.wakeFD >= 0 {
		fds = append(fds, unix.PollFd{Fd: int32(d.wakeFD), Events: unix.POLLIN})
	}
	for {
		n, err := unix.Poll(fds, timeoutMS)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return false, false, err
		}
		if n == 0 {
			return false, false, nil
		}
		if d.wakeFD >= 0 && fds[1].Revents&unix.POLLIN != 0 {
			var buf [8]byte
			_, _ = unix.Read(d.wakeFD, buf[:])
			woke = true
		}
		if fds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return false, woke, fmt.Errorf("V4L2 device error/disconnect (revents=0x%x)", fds[0].Revents)
		}
		frameReady = fds[0].Revents&unix.POLLIN != 0
		return frameReady, woke, nil
	}
}

func (d *linuxDevice) Wake() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.wakeFD < 0 {
		return
	}
	var one = [8]byte{1}
	_, _ = unix.Write(d.wakeFD, one[:])
}

func (d *linuxDevice) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	fd, wakeFD := d.fd, d.wakeFD
	d.fd, d.wakeFD = -1, -1
	d.mu.Unlock()

	var firstErr error
	if fd >= 0 {
		firstErr = unix.Close(fd)
	}
	if wakeFD >= 0 {
		if err := unix.Close(wakeFD); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ListDeviceInfos enumerates V4L2 video capture nodes under /dev/video*,
// filtering out metadata-only nodes created by uvcvideo.
func ListDeviceInfos() ([]DeviceInfo, error) {
	return listDeviceInfosWith(linuxSystem{})
}

// ListDevices returns display names ("Card Name (/dev/videoN)") for V4L2
// video capture devices.
func ListDevices() ([]string, error) {
	infos, err := ListDeviceInfos()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(infos))
	for i, d := range infos {
		out[i] = d.DisplayName()
	}
	return out, nil
}

// ListModes lists the supported capture modes of device.
func ListModes(device string) ([]Mode, error) {
	sys := linuxSystem{}
	path, err := resolveDevicePath(sys, device)
	if err != nil {
		return nil, err
	}
	dev, err := sys.Open(path, false)
	if err != nil {
		return nil, err
	}
	defer dev.Close()
	return listModesWith(dev)
}

// Start opens device, negotiates mode, maps >= 4 MMAP buffers, and invokes
// onFrame for each captured frame until Stop is called or an error occurs.
func Start(cfg Config, now func() int64, freq int64, onFrame func(capture.Frame)) (*Source, error) {
	return startWith(linuxSystem{}, cfg, now, freq, onFrame)
}
