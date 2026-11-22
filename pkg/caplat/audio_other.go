//go:build !windows && !linux

package caplat

func newAudioBackend() audioBackend { return unsupportedAudioBackend{} }
