//go:build linux

package alsa

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestLinuxTimespecLayout(t *testing.T) {
	var ut unix.Timespec
	var ts Timespec
	if unsafe.Sizeof(ts) != unsafe.Sizeof(ut) {
		t.Fatalf("Timespec size = %d, unix.Timespec = %d", unsafe.Sizeof(ts), unsafe.Sizeof(ut))
	}
	if unsafe.Offsetof(ts.Sec) != unsafe.Offsetof(ut.Sec) || unsafe.Offsetof(ts.Nsec) != unsafe.Offsetof(ut.Nsec) {
		t.Fatalf("Timespec offsets = (%d,%d), unix = (%d,%d)",
			unsafe.Offsetof(ts.Sec), unsafe.Offsetof(ts.Nsec),
			unsafe.Offsetof(ut.Sec), unsafe.Offsetof(ut.Nsec))
	}
}
