//go:build !windows && !linux

package capture

import "time"

var clockBase = time.Now()

func now() int64       { return 1 + time.Since(clockBase).Nanoseconds() }
func clockFreq() int64 { return 1_000_000_000 }
