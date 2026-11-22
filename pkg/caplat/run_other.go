//go:build !windows && !linux

package caplat

func defaultBackend() string      { return BackendFFmpeg }
func newVideoBackend() videoBackend { return unsupportedVideoBackend{} }
