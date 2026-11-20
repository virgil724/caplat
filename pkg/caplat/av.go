package caplat

import "math"

// AVResult is a simultaneous video and audio measurement through the same
// HDMI link. Both latencies are measured from when this PC put the frame or
// the sample out (DXGI vblank, IAudioClock position) on the same QPC clock,
// so their difference is the A/V skew the capture path adds.
type AVResult struct {
	Video Result
	Audio AudioResult
}

// Skew is audio p50 minus video p50 in ms: positive means the sound
// reaches caplat later than the picture it belongs to. NaN without data.
func (r AVResult) Skew() float64 {
	if r.Video.Total.N == 0 || r.Audio.Delivered.N == 0 {
		return math.NaN()
	}
	return r.Audio.Delivered.P50 - r.Video.Total.P50
}

// DriverSkew is the same at the drivers' capture timestamps (Media
// Foundation video, WASAPI audio); NaN when either has none.
func (r AVResult) DriverSkew() float64 {
	if r.Video.Device.N == 0 || r.Audio.Device.N == 0 {
		return math.NaN()
	}
	return r.Audio.Device.P50 - r.Video.Device.P50
}
