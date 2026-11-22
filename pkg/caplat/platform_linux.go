//go:build linux

package caplat

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/virgil724/caplat/capture"
	"github.com/virgil724/caplat/capture/alsa"
	"github.com/virgil724/caplat/capture/v4l2"
	"github.com/virgil724/caplat/displays"
	"github.com/virgil724/caplat/internal/audio"
	"github.com/virgil724/caplat/internal/capture/ffmpeg"
	"github.com/virgil724/caplat/internal/present"
)

func defaultBackend() string      { return BackendV4L2 }
func defaultAudioBackend() string { return AudioALSA }

func newVideoBackend() videoBackend { return linuxVideoBackend{} }
func newAudioBackend() audioBackend { return linuxAudioBackend{} }

type linuxVideoBackend struct{}

func (linuxVideoBackend) Displays() ([]Display, error) { return displays.List() }

func (linuxVideoBackend) Devices(ctx context.Context, ffmpegBin string) ([]string, error) {
	return ffmpeg.ListDevices(ctx, ffmpegBin)
}

func (linuxVideoBackend) DevicesMF() ([]string, error)    { return nil, ErrUnsupported }
func (linuxVideoBackend) DevicesDShow() ([]string, error) { return nil, ErrUnsupported }
func (linuxVideoBackend) DevicesV4L2() ([]string, error)  { return v4l2.ListDevices() }

// Modes lists the device's modes from V4L2 directly (ffmpeg's v4l2
// listing has no frame rates), adding formats only ffmpeg can use (mjpeg)
// from ffmpeg's listing, and falling back to ffmpeg entirely.
func (linuxVideoBackend) Modes(ctx context.Context, ffmpegBin, device string) ([]Mode, error) {
	ff, ffErr := ffmpeg.ListModes(ctx, ffmpegBin, device)
	ms, err := v4l2.ListModes(device)
	if err != nil || len(ms) == 0 {
		return ff, ffErr
	}
	return mergeModes(modesFromV4L2(ms), ff), nil
}

func (linuxVideoBackend) ValidateBackend(backend string) error {
	if backend != BackendV4L2 && backend != BackendFFmpeg {
		return fmt.Errorf("unknown backend %q (want %s or %s)", backend, BackendV4L2, BackendFFmpeg)
	}
	return nil
}

func (linuxVideoBackend) ValidatePresenter(presenter string) error {
	switch presenter {
	case "", PresenterAuto, PresenterWayland:
		return nil
	default:
		return fmt.Errorf("unknown presenter %q on Linux (want %s or %s)", presenter, PresenterAuto, PresenterWayland)
	}
}

func (linuxVideoBackend) ListCaptureDevices(ctx context.Context, cfg Config) ([]string, error) {
	if cfg.Backend == BackendV4L2 {
		return v4l2.ListDevices()
	}
	return ffmpeg.ListDevices(ctx, cfg.FFmpeg)
}

func readUVCHWTimestamps() string {
	b, err := os.ReadFile("/sys/module/uvcvideo/parameters/hwtimestamps")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func formatV4L2Mode(w, h int, rate, fourcc, hwTS string) string {
	meta := "V4L2"
	if hwTS != "" {
		meta = "V4L2, uvcvideo.hwtimestamps=" + hwTS
	}
	return fmt.Sprintf("%dx%d@%s %s (%s)", w, h, rate, fourcc, meta)
}

func (linuxVideoBackend) PrepareCapture(ctx context.Context, cfg Config, d Display, dev string) (Setup, captureStarter, error) {
	var s Setup
	if cfg.Backend == BackendV4L2 {
		vc := v4l2.Config{
			Device: dev,
			Width:  cfg.CaptureWidth,
			Height: cfg.CaptureHeight,
			FPS:    d.RefreshHz(),
			Format: cfg.PixelFormat,
		}
		hwTS := readUVCHWTimestamps()
		s.CaptureMode = formatV4L2Mode(cfg.CaptureWidth, cfg.CaptureHeight, d.FrameRate(), cfg.PixelFormat, hwTS)
		return s, v4l2Starter{cfg: vc}, nil
	}
	pf := cfg.PixelFormat
	if strings.EqualFold(pf, "auto") {
		modes, err := ffmpeg.ListModes(ctx, cfg.FFmpeg, dev)
		if err != nil {
			return Setup{}, nil, err
		}
		if pf, err = ffmpeg.PickFormat(modes, cfg.CaptureWidth, cfg.CaptureHeight, d.RefreshHz()); err != nil {
			return Setup{}, nil, fmt.Errorf("%s: %w", dev, err)
		}
	}
	ff := ffmpeg.Config{
		FFmpeg:      cfg.FFmpeg,
		InputFormat: "v4l2",
		Device:      dev,
		Width:       cfg.CaptureWidth,
		Height:      cfg.CaptureHeight,
		FrameRate:   d.FrameRate(),
		PixelFormat: pf,
		Preset:      cfg.Preset,
		ExtraIn:     cfg.FFmpegIn,
		ExtraOut:    cfg.FFmpegOut,
	}
	args, err := ffmpeg.Args(ff)
	if err != nil {
		return Setup{}, nil, err
	}
	s.CaptureMode = fmt.Sprintf("%dx%d@%s %s", ff.Width, ff.Height, ff.FrameRate, pf)
	s.Preset, s.FFmpegArgs = cfg.Preset, args
	return s, ffmpegStarter{cfg: ff}, nil
}

func (linuxVideoBackend) NewPresenter(string) present.Presenter { return present.NewWayland() }

type v4l2Starter struct{ cfg v4l2.Config }

func (st v4l2Starter) Start(_ context.Context, now func() int64, freq int64, onFrame func(capture.Frame)) (capture.Source, string, error) {
	s, err := v4l2.Start(st.cfg, now, freq, onFrame)
	if err != nil {
		return nil, "", err
	}
	mode := formatV4L2Mode(s.Mode.Width, s.Mode.Height, fmt.Sprintf("%.3f", s.Mode.FPS), s.Mode.FourCC, s.Timestamps.HWTimestamps)
	return s, mode, nil
}

type ffmpegStarter struct{ cfg ffmpeg.Config }

func (st ffmpegStarter) Start(ctx context.Context, now func() int64, _ int64, onFrame func(capture.Frame)) (capture.Source, string, error) {
	s, err := ffmpeg.Start(ctx, st.cfg, now, onFrame)
	return s, "", err
}

type linuxAudioBackend struct{}

func (linuxAudioBackend) ValidateBackend(backend string) error {
	if backend != AudioALSA && backend != AudioFFmpeg {
		return fmt.Errorf("unknown audio backend %q (want %s or %s)", backend, AudioALSA, AudioFFmpeg)
	}
	return nil
}

func (linuxAudioBackend) Outputs() ([]string, error) { return alsa.Outputs() }
func (linuxAudioBackend) Inputs() ([]string, error)  { return alsa.Inputs() }

func (linuxAudioBackend) StartCapture(ctx context.Context, cfg AudioConfig, input string, now func() int64, freq int64, onSamples audio.OnSamples) (audioSource, time.Duration, []string, error) {
	switch cfg.Backend {
	case AudioALSA:
		c, err := audio.StartALSA(input, cfg.Period, cfg.Exclusive, now, freq, onSamples)
		if err != nil {
			return nil, 0, nil, err
		}
		return c, c.Period, nil, nil
	case AudioFFmpeg:
		fc := audio.FFmpegConfig{
			FFmpeg:      cfg.FFmpeg,
			InputFormat: "alsa",
			Device:      input,
			LowLatency:  cfg.Preset != PresetDefault,
		}
		args := audio.FFmpegArgs(fc)
		c, err := audio.StartFFmpeg(ctx, fc, now, onSamples)
		if err != nil {
			return nil, 0, args, err
		}
		return c, 0, args, nil
	}
	return nil, 0, nil, fmt.Errorf("unknown audio backend %q (want %s or %s)", cfg.Backend, AudioALSA, AudioFFmpeg)
}

func (linuxAudioBackend) StartPlayer(cfg audio.PlayerConfig, freq int64, onEmit func(audio.Emitted)) (audioPlayer, int, error) {
	pl, err := audio.StartALSAPlayer(cfg, freq, onEmit)
	if err != nil {
		return nil, 0, err
	}
	return pl, pl.Rate, nil
}
