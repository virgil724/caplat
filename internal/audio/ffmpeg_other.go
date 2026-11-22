//go:build !windows

package audio

func defaultFFmpegInputFormat() string { return "alsa" }
