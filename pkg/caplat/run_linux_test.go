//go:build linux

package caplat

import "testing"

func TestLinuxDefaultsAndValidation(t *testing.T) {
	if got := DefaultBackend(); got != BackendV4L2 {
		t.Errorf("DefaultBackend() = %q, want %q", got, BackendV4L2)
	}
	if got := DefaultAudioBackend(); got != AudioALSA {
		t.Errorf("DefaultAudioBackend() = %q, want %q", got, AudioALSA)
	}
	var cfg Config
	cfg.defaults()
	if cfg.Backend != BackendV4L2 || cfg.Presenter != PresenterAuto {
		t.Errorf("Config.defaults() = (%q, %q)", cfg.Backend, cfg.Presenter)
	}

	vb := linuxVideoBackend{}
	for _, b := range []string{BackendV4L2, BackendFFmpeg} {
		if err := vb.ValidateBackend(b); err != nil {
			t.Errorf("ValidateBackend(%q) = %v", b, err)
		}
	}
	if err := vb.ValidateBackend(BackendMF); err == nil {
		t.Errorf("ValidateBackend(mf) succeeded on Linux")
	}

	for _, p := range []string{"", PresenterAuto, PresenterWayland} {
		if err := vb.ValidatePresenter(p); err != nil {
			t.Errorf("ValidatePresenter(%q) = %v", p, err)
		}
	}
	if err := vb.ValidatePresenter("drm"); err == nil {
		t.Errorf("ValidatePresenter(drm) succeeded unexpectedly")
	}
	if vb.NewPresenter(PresenterWayland) == nil {
		t.Errorf("NewPresenter(wayland) returned nil")
	}

	ab := linuxAudioBackend{}
	for _, b := range []string{AudioALSA, AudioFFmpeg} {
		if err := ab.ValidateBackend(b); err != nil {
			t.Errorf("audio ValidateBackend(%q) = %v", b, err)
		}
	}
	if err := ab.ValidateBackend(AudioWASAPI); err == nil {
		t.Errorf("audio ValidateBackend(wasapi) succeeded on Linux")
	}
}
