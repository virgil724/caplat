//go:build linux

package wayland

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// SocketPath returns the Wayland Unix socket path from $XDG_RUNTIME_DIR and
// $WAYLAND_DISPLAY (default "wayland-0").
func SocketPath() (string, error) {
	disp := os.Getenv("WAYLAND_DISPLAY")
	if disp == "" {
		disp = "wayland-0"
	}
	if filepath.IsAbs(disp) {
		return disp, nil
	}
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if rt == "" {
		return "", fmt.Errorf("XDG_RUNTIME_DIR is not set; cannot locate Wayland socket %q", disp)
	}
	return filepath.Join(rt, disp), nil
}

// Connect opens a connection to the user's Wayland compositor.
func Connect(warn func(string)) (*Client, error) {
	path, err := SocketPath()
	if err != nil {
		return nil, err
	}
	tr, err := DialUnix(path)
	if err != nil {
		return nil, fmt.Errorf("connect Wayland %s: %w", path, err)
	}
	return NewClient(tr, linuxSHM{}, warn), nil
}

// DialUnix connects to a Wayland Unix domain socket.
func DialUnix(path string) (Transport, error) {
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.Connect(fd, &unix.SockaddrUnix{Name: path}); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return &unixTransport{fd: fd}, nil
}

type unixTransport struct {
	fd     int
	closed bool
}

func wrapFD(fd int) *unixTransport { return &unixTransport{fd: fd} }

func (t *unixTransport) Send(data []byte, fds []int) error {
	var oob []byte
	if len(fds) > 0 {
		oob = unix.UnixRights(fds...)
	}
	for len(data) > 0 || len(oob) > 0 {
		n, err := unix.SendmsgN(t.fd, data, oob, nil, unix.MSG_NOSIGNAL)
		if err == unix.EINTR {
			continue
		}
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			_, _ = unix.Poll([]unix.PollFd{{Fd: int32(t.fd), Events: unix.POLLOUT}}, 1000)
			continue
		}
		if err != nil {
			return err
		}
		oob = nil // ancillary data is delivered on the first sendmsgN
		data = data[n:]
	}
	return nil
}

func (t *unixTransport) Recv(buf []byte, timeoutMS int) (int, []int, error) {
	pfd := []unix.PollFd{{Fd: int32(t.fd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(pfd, timeoutMS)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, nil, err
		}
		if n == 0 {
			return 0, nil, nil
		}
		break
	}
	if pfd[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0 && pfd[0].Revents&unix.POLLIN == 0 {
		return 0, nil, io.EOF
	}

	oob := make([]byte, unix.CmsgSpace(16*4))
	for {
		n, oobn, _, _, err := unix.Recvmsg(t.fd, buf, oob, unix.MSG_CMSG_CLOEXEC)
		if err == unix.EINTR {
			continue
		}
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			return 0, nil, nil
		}
		if err != nil {
			return 0, nil, err
		}
		if n == 0 {
			return 0, nil, io.EOF
		}
		var fds []int
		if oobn > 0 {
			cmsgs, perr := unix.ParseSocketControlMessage(oob[:oobn])
			if perr != nil {
				return n, nil, perr
			}
			for _, cm := range cmsgs {
				pfds, _ := unix.ParseUnixRights(&cm)
				fds = append(fds, pfds...)
			}
		}
		return n, fds, nil
	}
}

func (t *unixTransport) Close() error {
	if t.closed {
		return nil
	}
	t.closed = true
	return unix.Close(t.fd)
}

type linuxSHM struct{}

func (linuxSHM) Alloc(name string, size int) (int, []byte, func(), error) {
	fd, err := unix.MemfdCreate(name, unix.MFD_CLOEXEC)
	if err != nil {
		return -1, nil, nil, err
	}
	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		_ = unix.Close(fd)
		return -1, nil, nil, err
	}
	mem, err := unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		_ = unix.Close(fd)
		return -1, nil, nil, err
	}
	cleanup := func() {
		_ = unix.Munmap(mem)
	}
	return fd, mem, cleanup, nil
}

func (linuxSHM) CloseFD(fd int) {
	if fd >= 0 {
		_ = unix.Close(fd)
	}
}
