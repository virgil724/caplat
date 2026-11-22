//go:build !windows && !linux

package caplat

func defaultAudioBackend() string { return AudioFFmpeg }
func newAudioBackend() audioBackend { return unsupportedAudioBackend{} }
