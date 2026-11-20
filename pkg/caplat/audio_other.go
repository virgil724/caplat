//go:build !windows

package caplat

import "context"

// AudioOutputs lists audio outputs.
func AudioOutputs() ([]string, error) { return nil, ErrUnsupported }

// AudioInputs lists audio inputs.
func AudioInputs() ([]string, error) { return nil, ErrUnsupported }

// RunAudio measures audio latency.
func RunAudio(ctx context.Context, cfg AudioConfig) (AudioResult, error) {
	return AudioResult{}, ErrUnsupported
}

// RunAV measures video and audio together.
func RunAV(ctx context.Context, v Config, a AudioConfig, updates chan<- Update) (AVResult, error) {
	return AVResult{}, ErrUnsupported
}
