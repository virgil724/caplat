// Package alsa captures and plays PCM audio on Linux via libasound.so.2 loaded
// dynamically with purego (CGO_ENABLED=0, zero C callbacks).
//
// Both Capture and Playback enable SND_PCM_TSTAMP_ENABLE with
// SND_PCM_TSTAMP_TYPE_MONOTONIC so driver htstamp timestamps land directly on
// Linux CLOCK_MONOTONIC nanoseconds. When opening a raw hw:X,Y PCM fails with
// EBUSY because PipeWire or PulseAudio owns the device, FallbackDefault allows
// falling back to "pipewire" or "default" while marking SoftwareMixer = true.
package alsa
