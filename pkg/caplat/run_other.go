//go:build !windows && !linux

package caplat

func newVideoBackend() videoBackend { return unsupportedVideoBackend{} }
