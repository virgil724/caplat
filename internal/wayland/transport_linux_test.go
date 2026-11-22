//go:build linux

package wayland

import (
	"bytes"
	"io"
	"testing"

	"golang.org/x/sys/unix"
)

func TestUnixTransportSocketpairSCMRights(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("Socketpair: %v", err)
	}
	clientTr := wrapFD(fds[0])
	serverTr := wrapFD(fds[1])
	defer clientTr.Close()
	defer serverTr.Close()

	shm := linuxSHM{}
	memfd, data, cleanup, err := shm.Alloc("caplat-scm-test", 4096)
	if err != nil {
		t.Fatalf("MemfdCreate: %v", err)
	}
	defer cleanup()
	wantPayload := []byte("CAPLAT-WAYLAND-SHM-TEST")
	copy(data, wantPayload)

	var enc Encoder
	enc.Msg(3, wlShmReqCreatePool, func(e *Encoder) {
		e.Uint32(4)
		e.FD(memfd)
		e.Int32(4096)
	})
	wireBytes, sendFDs := enc.Bytes()
	if err := clientTr.Send(wireBytes, sendFDs); err != nil {
		shm.CloseFD(memfd)
		t.Fatalf("Send: %v", err)
	}
	shm.CloseFD(memfd)

	rxBuf := make([]byte, 1024)
	n, rxFDs, err := serverTr.Recv(rxBuf, 1000)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if n != len(wireBytes) || !bytes.Equal(rxBuf[:n], wireBytes) {
		t.Fatalf("wire bytes mismatch: got %d want %d", n, len(wireBytes))
	}
	if len(rxFDs) != 1 {
		t.Fatalf("got %d FDs, want 1", len(rxFDs))
	}
	defer unix.Close(rxFDs[0])

	gotMem := make([]byte, len(wantPayload))
	if _, err := unix.Pread(rxFDs[0], gotMem, 0); err != nil && err != io.EOF {
		t.Fatalf("Pread received memfd: %v", err)
	}
	if !bytes.Equal(gotMem, wantPayload) {
		t.Fatalf("received memfd content = %q, want %q", gotMem, wantPayload)
	}
}
