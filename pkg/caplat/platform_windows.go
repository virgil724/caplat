//go:build windows

package caplat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/virgil724/caplat/capture"
	"github.com/virgil724/caplat/capture/mf"
	"github.com/virgil724/caplat/internal/audio"
	"github.com/virgil724/caplat/internal/capture/dshow"
	"github.com/virgil724/caplat/internal/capture/ffmpeg"
	"github.com/virgil724/caplat/displays"
	"github.com/virgil724/caplat/internal/present"
	"github.com/virgil724/caplat/internal/win"
)

func newVideoBackend() videoBackend { return windowsVideoBackend{} }
func newAudioBackend() audioBackend { return windowsAudioBackend{} }

type windowsVideoBackend struct{}

func (windowsVideoBackend) Displays() ([]Display, error) {
	win.SetDPIAware()
	return displays.List()
}

func (windowsVideoBackend) Devices(ctx context.Context, ffmpegBin string) ([]string, error) {
	return ffmpeg.ListDevices(ctx, ffmpegBin)
}

func (windowsVideoBackend) DevicesMF() ([]string, error)    { return mf.ListDevices() }
func (windowsVideoBackend) DevicesDShow() ([]string, error) { return dshow.ListDevices() }

func (windowsVideoBackend) Modes(ctx context.Context, ffmpegBin, device string) ([]Mode, error) {
	return ffmpeg.ListModes(ctx, ffmpegBin, device)
}

func (windowsVideoBackend) ValidateBackend(backend string) error {
	if backend != BackendFFmpeg && backend != BackendMF && backend != BackendDShow {
		return fmt.Errorf("unknown backend %q (want %s, %s or %s)", backend, BackendMF, BackendDShow, BackendFFmpeg)
	}
	return nil
}

func (windowsVideoBackend) ListCaptureDevices(ctx context.Context, cfg Config) ([]string, error) {
	switch cfg.Backend {
	case BackendMF:
		return mf.ListDevices()
	case BackendDShow:
		return dshow.ListDevices()
	}
	return ffmpeg.ListDevices(ctx, cfg.FFmpeg)
}

func apiName(backend string) string {
	if backend == BackendDShow {
		return "DirectShow"
	}
	return "Media Foundation"
}

func (windowsVideoBackend) PrepareCapture(ctx context.Context, cfg Config, d Display, dev string) (Setup, captureStarter, error) {
	var s Setup
	if cfg.Backend == BackendMF || cfg.Backend == BackendDShow {
		mc := mf.Config{Device: dev, Width: cfg.CaptureWidth, Height: cfg.CaptureHeight, FPS: d.RefreshHz(), Format: cfg.PixelFormat}
		s.CaptureMode = fmt.Sprintf("%dx%d@%s %s (%s)", cfg.CaptureWidth, cfg.CaptureHeight, d.FrameRate(), cfg.PixelFormat, apiName(cfg.Backend))
		if cfg.Backend == BackendDShow {
			return s, dshowStarter{cfg: mc}, nil
		}
		return s, mfStarter{cfg: mc}, nil
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
		FFmpeg: cfg.FFmpeg, Device: dev, Width: cfg.CaptureWidth, Height: cfg.CaptureHeight,
		FrameRate: d.FrameRate(), PixelFormat: pf, Preset: cfg.Preset, ExtraIn: cfg.FFmpegIn, ExtraOut: cfg.FFmpegOut,
	}
	args, err := ffmpeg.Args(ff)
	if err != nil {
		return Setup{}, nil, err
	}
	s.CaptureMode = fmt.Sprintf("%dx%d@%s %s", ff.Width, ff.Height, ff.FrameRate, pf)
	s.Preset, s.FFmpegArgs = cfg.Preset, args
	return s, ffmpegStarter{cfg: ff}, nil
}

func (windowsVideoBackend) NewPresenter() present.Presenter { return present.New() }

type dshowStarter struct{ cfg mf.Config }

func (st dshowStarter) Start(_ context.Context, now func() int64, _ int64, onFrame func(capture.Frame)) (capture.Source, string, error) {
	c := st.cfg
	s, err := dshow.Start(dshow.Config{Device: c.Device, Width: c.Width, Height: c.Height, FPS: c.FPS, Format: c.Format}, now, onFrame)
	if err != nil {
		return nil, "", err
	}
	mode := fmt.Sprintf("%dx%d@%.3f %s (DirectShow)", s.Mode.Width, s.Mode.Height, s.Mode.FPS, s.Mode.FourCC)
	return s, mode, nil
}

type mfStarter struct{ cfg mf.Config }

func (st mfStarter) Start(_ context.Context, now func() int64, freq int64, onFrame func(capture.Frame)) (capture.Source, string, error) {
	s, err := mf.Start(st.cfg, now, freq, onFrame)
	if err != nil {
		return nil, "", err
	}
	mode := fmt.Sprintf("%dx%d@%.3f %s (Media Foundation)", s.Mode.Width, s.Mode.Height, s.Mode.FPS, s.Mode.FourCC)
	return s, mode, nil
}

type ffmpegStarter struct{ cfg ffmpeg.Config }

func (st ffmpegStarter) Start(ctx context.Context, now func() int64, _ int64, onFrame func(capture.Frame)) (capture.Source, string, error) {
	s, err := ffmpeg.Start(ctx, st.cfg, now, onFrame)
	return s, "", err
}

type windowsAudioBackend struct{}

func (windowsAudioBackend) ValidateBackend(backend string) error {
	if backend != AudioWASAPI && backend != AudioFFmpeg {
		return fmt.Errorf("unknown audio backend %q (want %s or %s)", backend, AudioWASAPI, AudioFFmpeg)
	}
	return nil
}

func (windowsAudioBackend) Outputs() ([]string, error) { return audio.Endpoints(false) }
func (windowsAudioBackend) Inputs() ([]string, error)  { return audio.Endpoints(true) }

func (windowsAudioBackend) StartCapture(ctx context.Context, cfg AudioConfig, input string, now func() int64, freq int64, onSamples audio.OnSamples) (audioSource, time.Duration, []string, error) {
	switch cfg.Backend {
	case AudioWASAPI:
		c, err := audio.StartWASAPI(input, cfg.Period, cfg.Exclusive, now, freq, onSamples)
		if err != nil {
			return nil, 0, nil, err
		}
		return c, c.Period, nil, nil
	case AudioFFmpeg:
		fc := audio.FFmpegConfig{FFmpeg: cfg.FFmpeg, Device: input, LowLatency: cfg.Preset != PresetDefault}
		args := audio.FFmpegArgs(fc)
		c, err := audio.StartFFmpeg(ctx, fc, now, onSamples)
		if err != nil {
			return nil, 0, args, err
		}
		return c, 0, args, nil
	}
	return nil, 0, nil, fmt.Errorf("unknown audio backend %q (want %s or %s)", cfg.Backend, AudioWASAPI, AudioFFmpeg)
}

func (windowsAudioBackend) StartPlayer(cfg audio.PlayerConfig, freq int64, onEmit func(audio.Emitted)) (audioPlayer, int, error) {
	pl, err := audio.StartPlayer(cfg, freq, onEmit)
	if err != nil {
		return nil, 0, err
	}
	return pl, pl.Rate, nil
}
