package audio

import "github.com/virgil724/caplat/capture/wasapi"

// Packet is a run of captured samples.
type Packet struct {
	Start  int64 // index of its first sample in the stream
	Frames int
	// DeviceQPC is when its first sample was captured, per the driver, in
	// platform monotonic ticks (QPC on Windows, CLOCK_MONOTONIC ns on Linux),
	// or 0 if unknown.
	DeviceQPC int64
	// Arrive is when caplat got it, in platform monotonic ticks.
	Arrive int64
}

// OnSamples receives mono samples (the first channel) and their packet.
type OnSamples func(x []float64, p Packet)

// Emitted is one chirp as it left the output: its first sample's timestamp.
type Emitted struct {
	N   int   // chirp number, from 0
	QPC int64 // when its first sample was played, in platform monotonic ticks
}

// PlayerConfig describes the test signal.
type PlayerConfig struct {
	Endpoint string
	Chirp    func(rate int) []float64 // the chirp at the endpoint's rate
	Period   float64                  // seconds between chirps
	First    float64                  // seconds before the first chirp
	Level    float64                  // peak amplitude, 0..1
	// Jitter adds a random 0..Jitter seconds to each period, so chirps
	// fall at every phase of the capture's packet period instead of
	// always the same one.
	Jitter float64
}

// FindName returns the one name equal to want, or else containing it.
func FindName(names []string, want string) (string, error) { return wasapi.FindName(names, want) }
