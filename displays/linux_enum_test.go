package displays

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

func TestDRMLayoutsAndIOCTLs(t *testing.T) {
	cases := []struct {
		name      string
		got, want uintptr
	}{
		{"drmModeCardRes", unsafe.Sizeof(drmModeCardRes{}), 64},
		{"drmModeCardRes.CountFBs", unsafe.Offsetof(drmModeCardRes{}.CountFBs), 32},
		{"drmModeGetConnector", unsafe.Sizeof(drmModeGetConnector{}), 80},
		{"drmModeGetConnector.CountModes", unsafe.Offsetof(drmModeGetConnector{}.CountModes), 32},
		{"drmModeGetConnector.Connection", unsafe.Offsetof(drmModeGetConnector{}.Connection), 60},
		{"drmModeGetEncoder", unsafe.Sizeof(drmModeGetEncoder{}), 20},
		{"drmModeModeInfo", unsafe.Sizeof(drmModeModeInfo{}), 68},
		{"drmModeModeInfo.VRefresh", unsafe.Offsetof(drmModeModeInfo{}.VRefresh), 24},
		{"drmModeModeInfo.Name", unsafe.Offsetof(drmModeModeInfo{}.Name), 36},
		{"drmModeCRTC", unsafe.Sizeof(drmModeCRTC{}), 104},
		{"drmModeCRTC.ModeValid", unsafe.Offsetof(drmModeCRTC{}.ModeValid), 32},
		{"drmModeCRTC.Mode", unsafe.Offsetof(drmModeCRTC{}.Mode), 36},
		{"ioctl_GETRESOURCES", drmIoctlModeGetResources, 0xC04064A0},
		{"ioctl_GETCRTC", drmIoctlModeGetCRTC, 0xC06864A1},
		{"ioctl_GETENCODER", drmIoctlModeGetEncoder, 0xC01464A6},
		{"ioctl_GETCONNECTOR", drmIoctlModeGetConnector, 0xC05064A7},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %#x (%d), want %#x (%d)", tc.name, tc.got, tc.got, tc.want, tc.want)
		}
	}
}

func TestModeRefresh(t *testing.T) {
	cases := []struct {
		name           string
		mode           drmModeModeInfo
		wantN, wantD   uint32
	}{
		{
			name:  "1080p59.94_quantized_clock",
			mode:  drmModeModeInfo{Clock: 148352, HTotal: 2200, VTotal: 1125, VRefresh: 60},
			wantN: 60000, wantD: 1001,
		},
		{
			name:  "1080p60",
			mode:  drmModeModeInfo{Clock: 148500, HTotal: 2200, VTotal: 1125, VRefresh: 60},
			wantN: 60, wantD: 1,
		},
		{
			name:  "2160p59.94",
			mode:  drmModeModeInfo{Clock: 593407, HTotal: 4400, VTotal: 2250, VRefresh: 60},
			wantN: 60000, wantD: 1001,
		},
		{
			name:  "1440p165",
			mode:  drmModeModeInfo{Clock: 653560, HTotal: 2720, VTotal: 1456, VRefresh: 165},
			wantN: 165, wantD: 1,
		},
		{
			name:  "1080i59.94",
			mode:  drmModeModeInfo{Clock: 74176, HTotal: 2200, VTotal: 1125, Flags: drmModeInterlace, VRefresh: 60},
			wantN: 60000, wantD: 1001,
		},
		{
			name:  "fallback_vrefresh",
			mode:  drmModeModeInfo{VRefresh: 75},
			wantN: 75, wantD: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, d := modeRefresh(tc.mode)
			if n != tc.wantN || d != tc.wantD {
				t.Errorf("modeRefresh = %d/%d, want %d/%d", n, d, tc.wantN, tc.wantD)
			}
		})
	}
}

type fakeDRMCard struct {
	conns  map[string]drmConnectorState
	closed bool
}

func (f *fakeDRMCard) ActiveConnectors() (map[string]drmConnectorState, error) {
	return f.conns, nil
}
func (f *fakeDRMCard) Close() error { f.closed = true; return nil }

func writeConnectorDir(t *testing.T, root, name, status, enabled, modes string, edid []byte) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if enabled != "" {
		if err := os.WriteFile(filepath.Join(dir, "enabled"), []byte(enabled+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if modes != "" {
		if err := os.WriteFile(filepath.Join(dir, "modes"), []byte(modes), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if len(edid) > 0 {
		if err := os.WriteFile(filepath.Join(dir, "edid"), edid, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListLinuxWaylandZeroOriginCRTCs(t *testing.T) {
	sysfs := t.TempDir()
	writeConnectorDir(t, sysfs, "card0-DP-1", "connected", "enabled", "2560x1440\n",
		buildTestEDID("MSI", 0x3CC0, 1, "MAG274QRF-QD", ""))
	writeConnectorDir(t, sysfs, "card0-HDMI-A-1", "connected", "enabled", "3840x2160\n",
		buildTestEDID("AVT", 0x0553, 2, "AVT GC553", ""))
	writeConnectorDir(t, sysfs, "card0-DP-2", "disconnected", "disabled", "", nil)
	writeConnectorDir(t, sysfs, "card0-HDMI-A-2", "connected", "disabled", "1920x1080\n",
		buildTestEDID("DEL", 0x1111, 3, "DisabledMon", ""))
	// Non-connector entries should be skipped.
	if err := os.MkdirAll(filepath.Join(sysfs, "card0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sysfs, "renderD128"), 0o755); err != nil {
		t.Fatal(err)
	}

	fakeCard := &fakeDRMCard{
		conns: map[string]drmConnectorState{
			// Wayland compositors leave CRTC X/Y = 0 on all outputs.
			"DP-1": {
				X: 0, Y: 0, ModeValid: true,
				Mode: drmModeModeInfo{Clock: 653560, HDisplay: 2560, HTotal: 2720, VDisplay: 1440, VTotal: 1456},
			},
			"HDMI-A-1": {
				X: 0, Y: 0, ModeValid: true,
				Mode: drmModeModeInfo{Clock: 593407, HDisplay: 3840, HTotal: 4400, VDisplay: 2160, VTotal: 2250},
			},
		},
	}

	list, err := listLinux(sysfs, func(cardPath string) (drmCard, error) {
		if cardPath != "/dev/dri/card0" {
			t.Fatalf("unexpected card path %q", cardPath)
		}
		return fakeCard, nil
	})
	if err != nil {
		t.Fatalf("listLinux: %v", err)
	}
	if !fakeCard.closed {
		t.Error("DRM device was not closed")
	}
	if len(list) != 2 {
		t.Fatalf("got %d displays, want 2: %+v", len(list), list)
	}
	if list[0].Name != "DP-1" || !list[0].Primary || list[0].LikelyCard || list[0].Mode() != "2560x1440@165.000" {
		t.Errorf("display[0] = %+v", list[0])
	}
	if list[1].Name != "HDMI-A-1" || list[1].Primary || !list[1].LikelyCard || list[1].FrameRate() != "60000/1001" {
		t.Errorf("display[1] = %+v", list[1])
	}
	autoDisp, err := Find(list, "auto")
	if err != nil || autoDisp.Name != "HDMI-A-1" {
		t.Fatalf("Find(auto) = (%+v, %v), want HDMI-A-1", autoDisp, err)
	}
}

func TestListLinuxX11OffsetsAndSysfsFallback(t *testing.T) {
	sysfs := t.TempDir()
	writeConnectorDir(t, sysfs, "card0-HDMI-A-1", "connected", "enabled", "1920x1080\n",
		buildTestEDID("AVT", 0x0553, 10, "AVT GC553", ""))
	writeConnectorDir(t, sysfs, "card0-DP-1", "connected", "enabled", "2560x1440\n",
		buildTestEDID("MSI", 0x3CC0, 11, "MAG274QRF-QD", ""))
	// Card1 with no /dev/dri permission -> falls back to sysfs modes.
	writeConnectorDir(t, sysfs, "card1-HDMI-A-2", "connected", "enabled", "1280x720\n", nil)

	list, err := listLinux(sysfs, func(cardPath string) (drmCard, error) {
		if cardPath == "/dev/dri/card0" {
			return &fakeDRMCard{conns: map[string]drmConnectorState{
				"DP-1":     {X: 0, Y: 0, ModeValid: true, Mode: drmModeModeInfo{Clock: 148500, HDisplay: 2560, HTotal: 2720, VDisplay: 1440, VTotal: 1456}},
				"HDMI-A-1": {X: 2560, Y: 0, ModeValid: true, Mode: drmModeModeInfo{Clock: 148500, HDisplay: 1920, HTotal: 2200, VDisplay: 1080, VTotal: 1125}},
			}}, nil
		}
		return nil, os.ErrPermission
	})
	if err != nil {
		t.Fatalf("listLinux: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("got %d displays: %+v", len(list), list)
	}
	for _, d := range list {
		switch d.Name {
		case "DP-1":
			if !d.Primary {
				t.Errorf("DP-1 at (0,0) should be Primary when offset CRTC exists")
			}
		case "HDMI-A-1":
			if d.Primary || !d.LikelyCard || d.Rect.Left != 2560 {
				t.Errorf("HDMI-A-1 = %+v", d)
			}
		case "HDMI-A-2":
			if d.Width != 1280 || d.Height != 720 {
				t.Errorf("HDMI-A-2 fallback = %+v", d)
			}
		}
	}
}
