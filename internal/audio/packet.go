package audio

// Packet is a run of captured samples.
type Packet struct {
	Start  int64 // index of its first sample in the stream
	Frames int
	// DeviceQPC is when its first sample was captured, per the driver (QPC
	// ticks; 0 if unknown).
	DeviceQPC int64
	// Arrive is when caplat got it (QPC ticks).
	Arrive int64
}

// OnSamples receives mono samples (the first channel) and their packet.
type OnSamples func(x []float64, p Packet)
