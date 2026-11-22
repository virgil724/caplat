//go:build windows

package capture

import "github.com/virgil724/caplat/internal/win"

func now() int64       { return win.QPC() }
func clockFreq() int64 { return win.QPCFrequency() }

// QPC reads QueryPerformanceCounter, the clock every capture timestamp is
// on; QPCFrequency is its ticks per second.
func QPC() int64          { return win.QPC() }
func QPCFrequency() int64 { return win.QPCFrequency() }
