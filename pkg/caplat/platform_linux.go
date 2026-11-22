//go:build linux

package caplat

func newVideoBackend() videoBackend { return unsupportedVideoBackend{} }
func newAudioBackend() audioBackend { return unsupportedAudioBackend{} }
