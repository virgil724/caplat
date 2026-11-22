package capture

// Now returns the current time in platform monotonic ticks (QPC on Windows,
// CLOCK_MONOTONIC ns on Linux).
func Now() int64 { return now() }

// ClockFreq returns the platform monotonic clock frequency in ticks per second.
func ClockFreq() int64 { return clockFreq() }
