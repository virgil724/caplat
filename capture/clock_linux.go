//go:build linux

package capture

import "golang.org/x/sys/unix"

func now() int64 {
	var ts unix.Timespec
	_ = unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts)
	return ts.Nano()
}

func clockFreq() int64 { return 1_000_000_000 }
