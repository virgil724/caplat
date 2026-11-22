//go:build linux

package v4l2

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestLinuxTimevalLayout(t *testing.T) {
	var tv unix.Timeval
	var b v4l2Buffer
	if unsafe.Sizeof(tv) != 16 {
		t.Fatalf("unix.Timeval size = %d, want 16", unsafe.Sizeof(tv))
	}
	if unsafe.Offsetof(tv.Sec) != 0 || unsafe.Offsetof(tv.Usec) != 8 {
		t.Fatalf("unix.Timeval offsets = (%d,%d), want (0,8)", unsafe.Offsetof(tv.Sec), unsafe.Offsetof(tv.Usec))
	}
	if unsafe.Offsetof(b.TimestampSec) != 24 || unsafe.Offsetof(b.TimestampUsec) != 32 {
		t.Fatalf("v4l2Buffer timestamp offsets = (%d,%d), want (24,32)", unsafe.Offsetof(b.TimestampSec), unsafe.Offsetof(b.TimestampUsec))
	}
}
