//go:build !windows

package caplat

import (
	"context"
	"errors"
)

// ErrUnsupported is returned on platforms without a backend yet.
var ErrUnsupported = errors.New("caplat currently supports Windows only")

// ErrAborted is returned when the user stopped the run from the window.
var ErrAborted = errors.New("aborted")

// Display is one desktop output.
type Display struct {
	Name, FriendlyName  string
	Width, Height       int
	Primary, LikelyCard bool
}

// Mode describes the output mode.
func (d Display) Mode() string { return "" }

// Mode is one capture mode of a device.
type Mode struct {
	Format         string
	MinW, MinH     int
	MaxW, MaxH     int
	MinFPS, MaxFPS float64
}

// Displays lists the desktop's outputs.
func Displays() ([]Display, error) { return nil, ErrUnsupported }

// Devices lists capture devices.
func Devices(context.Context, string) ([]string, error) { return nil, ErrUnsupported }

// PickDevice chooses the capture card among devices.
func PickDevice([]string) string { return "" }

// Modes lists a device's capture modes.
func Modes(context.Context, string, string) ([]Mode, error) { return nil, ErrUnsupported }

// Prepare resolves a configuration without measuring.
func Prepare(context.Context, Config) (Setup, error) { return Setup{}, ErrUnsupported }

// Run measures.
func Run(context.Context, Config, chan<- Update) (Result, error) { return Result{}, ErrUnsupported }
