//go:build linux

package alsa

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

type alsaLib struct {
	fnOpen     func(pcm *uintptr, name string, stream int32, mode int32) int32
	fnClose    func(pcm uintptr) int32
	fnPrepare  func(pcm uintptr) int32
	fnStart    func(pcm uintptr) int32
	fnWait     func(pcm uintptr, timeout int32) int32
	fnReadi    func(pcm uintptr, buf unsafe.Pointer, size uint64) int64
	fnWritei   func(pcm uintptr, buf unsafe.Pointer, size uint64) int64
	fnRecover  func(pcm uintptr, errCode int32, silent int32) int32
	fnStrerror func(errCode int32) string

	fnHWParamsMalloc           func(ptr *uintptr) int32
	fnHWParamsFree             func(obj uintptr)
	fnHWParamsAny              func(pcm uintptr, params uintptr) int32
	fnHWParamsSetAccess        func(pcm uintptr, params uintptr, access int32) int32
	fnHWParamsSetFormat        func(pcm uintptr, params uintptr, format int32) int32
	fnHWParamsSetChannels      func(pcm uintptr, params uintptr, val uint32) int32
	fnHWParamsSetChannelsNear  func(pcm uintptr, params uintptr, val *uint32) int32
	fnHWParamsSetRateNear      func(pcm uintptr, params uintptr, val *uint32, dir *int32) int32
	fnHWParamsSetPeriodTimeNear func(pcm uintptr, params uintptr, val *uint32, dir *int32) int32
	fnHWParamsSetBufferTimeNear func(pcm uintptr, params uintptr, val *uint32, dir *int32) int32
	fnHWParamsGetPeriodSize    func(params uintptr, frames *uint64, dir *int32) int32
	fnHWParamsGetBufferSize    func(params uintptr, val *uint64) int32
	fnHWParams                 func(pcm uintptr, params uintptr) int32

	fnSWParamsMalloc            func(ptr *uintptr) int32
	fnSWParamsFree              func(obj uintptr)
	fnSWParamsCurrent           func(pcm uintptr, params uintptr) int32
	fnSWParamsSetTstampMode     func(pcm uintptr, params uintptr, val int32) int32
	fnSWParamsSetTstampType     func(pcm uintptr, params uintptr, val int32) int32
	fnSWParamsSetStartThreshold func(pcm uintptr, params uintptr, val uint64) int32
	fnSWParams                  func(pcm uintptr, params uintptr) int32

	fnStatusMalloc     func(ptr *uintptr) int32
	fnStatusFree       func(obj uintptr)
	fnStatus           func(pcm uintptr, status uintptr) int32
	fnStatusGetHtstamp func(status uintptr, ts *Timespec)
	fnStatusGetAvail   func(status uintptr) uint64
	fnStatusGetDelay   func(status uintptr) int64
}

var (
	libOnce sync.Once
	libInst *alsaLib
	libErr  error
)

func loadALSA() (*alsaLib, error) {
	libOnce.Do(func() {
		var handle uintptr
		var err error
		for _, so := range []string{"libasound.so.2", "libasound.so"} {
			handle, err = purego.Dlopen(so, purego.RTLD_LAZY|purego.RTLD_LOCAL)
			if err == nil {
				break
			}
		}
		if handle == 0 {
			libErr = fmt.Errorf("load libasound.so.2: %w (install alsa-lib / libasound2)", err)
			return
		}
		l := &alsaLib{}
		reg := func(fptr any, name string) bool {
			if libErr != nil {
				return false
			}
			sym, err := purego.Dlsym(handle, name)
			if err != nil {
				libErr = fmt.Errorf("libasound symbol %s: %w", name, err)
				return false
			}
			purego.RegisterFunc(fptr, sym)
			return true
		}
		reg(&l.fnOpen, "snd_pcm_open")
		reg(&l.fnClose, "snd_pcm_close")
		reg(&l.fnPrepare, "snd_pcm_prepare")
		reg(&l.fnStart, "snd_pcm_start")
		reg(&l.fnWait, "snd_pcm_wait")
		reg(&l.fnReadi, "snd_pcm_readi")
		reg(&l.fnWritei, "snd_pcm_writei")
		reg(&l.fnRecover, "snd_pcm_recover")
		reg(&l.fnStrerror, "snd_strerror")

		reg(&l.fnHWParamsMalloc, "snd_pcm_hw_params_malloc")
		reg(&l.fnHWParamsFree, "snd_pcm_hw_params_free")
		reg(&l.fnHWParamsAny, "snd_pcm_hw_params_any")
		reg(&l.fnHWParamsSetAccess, "snd_pcm_hw_params_set_access")
		reg(&l.fnHWParamsSetFormat, "snd_pcm_hw_params_set_format")
		reg(&l.fnHWParamsSetChannels, "snd_pcm_hw_params_set_channels")
		reg(&l.fnHWParamsSetChannelsNear, "snd_pcm_hw_params_set_channels_near")
		reg(&l.fnHWParamsSetRateNear, "snd_pcm_hw_params_set_rate_near")
		reg(&l.fnHWParamsSetPeriodTimeNear, "snd_pcm_hw_params_set_period_time_near")
		reg(&l.fnHWParamsSetBufferTimeNear, "snd_pcm_hw_params_set_buffer_time_near")
		reg(&l.fnHWParamsGetPeriodSize, "snd_pcm_hw_params_get_period_size")
		reg(&l.fnHWParamsGetBufferSize, "snd_pcm_hw_params_get_buffer_size")
		reg(&l.fnHWParams, "snd_pcm_hw_params")

		reg(&l.fnSWParamsMalloc, "snd_pcm_sw_params_malloc")
		reg(&l.fnSWParamsFree, "snd_pcm_sw_params_free")
		reg(&l.fnSWParamsCurrent, "snd_pcm_sw_params_current")
		reg(&l.fnSWParamsSetTstampMode, "snd_pcm_sw_params_set_tstamp_mode")
		reg(&l.fnSWParamsSetTstampType, "snd_pcm_sw_params_set_tstamp_type")
		reg(&l.fnSWParamsSetStartThreshold, "snd_pcm_sw_params_set_start_threshold")
		reg(&l.fnSWParams, "snd_pcm_sw_params")

		reg(&l.fnStatusMalloc, "snd_pcm_status_malloc")
		reg(&l.fnStatusFree, "snd_pcm_status_free")
		reg(&l.fnStatus, "snd_pcm_status")
		reg(&l.fnStatusGetHtstamp, "snd_pcm_status_get_htstamp")
		reg(&l.fnStatusGetAvail, "snd_pcm_status_get_avail")
		reg(&l.fnStatusGetDelay, "snd_pcm_status_get_delay")

		if libErr == nil {
			libInst = l
		}
	})
	return libInst, libErr
}

type puregoPCM struct {
	pcm    uintptr
	status uintptr
}

type puregoDriver struct {
	lib *alsaLib
}

func newPuregoDriver() (*puregoDriver, error) {
	lib, err := loadALSA()
	if err != nil {
		return nil, err
	}
	return &puregoDriver{lib: lib}, nil
}

func (d *puregoDriver) errStr(code int32) error {
	return fmt.Errorf("%s (%d)", d.lib.fnStrerror(code), code)
}

func (d *puregoDriver) Open(name string, stream int32, mode int32) (pcmHandle, int32, error) {
	var pcm uintptr
	rc := d.lib.fnOpen(&pcm, name, stream, mode)
	if rc < 0 {
		return nil, rc, d.errStr(rc)
	}
	var st uintptr
	if rc2 := d.lib.fnStatusMalloc(&st); rc2 < 0 {
		d.lib.fnClose(pcm)
		return nil, rc2, d.errStr(rc2)
	}
	return &puregoPCM{pcm: pcm, status: st}, 0, nil
}

func (d *puregoDriver) Close(h pcmHandle) error {
	p := h.(*puregoPCM)
	if p.status != 0 {
		d.lib.fnStatusFree(p.status)
		p.status = 0
	}
	if p.pcm != 0 {
		rc := d.lib.fnClose(p.pcm)
		p.pcm = 0
		if rc < 0 {
			return d.errStr(rc)
		}
	}
	return nil
}

func (d *puregoDriver) Configure(h pcmHandle, cfg hwConfig, isPlayback bool) (negotiatedHW, error) {
	p := h.(*puregoPCM)
	var hw uintptr
	if rc := d.lib.fnHWParamsMalloc(&hw); rc < 0 {
		return negotiatedHW{}, d.errStr(rc)
	}
	defer d.lib.fnHWParamsFree(hw)

	if rc := d.lib.fnHWParamsAny(p.pcm, hw); rc < 0 {
		return negotiatedHW{}, fmt.Errorf("hw_params_any: %w", d.errStr(rc))
	}
	if rc := d.lib.fnHWParamsSetAccess(p.pcm, hw, AccessRWInterleaved); rc < 0 {
		return negotiatedHW{}, fmt.Errorf("set_access: %w", d.errStr(rc))
	}

	var chosenFmt int32 = -1
	for _, f := range []int32{FormatFloatLE, FormatS32LE, FormatS24LE, FormatS24_3LE, FormatS16LE} {
		if d.lib.fnHWParamsSetFormat(p.pcm, hw, f) >= 0 {
			chosenFmt = f
			break
		}
	}
	if chosenFmt < 0 {
		return negotiatedHW{}, fmt.Errorf("no supported ALSA sample format")
	}

	ch := uint32(cfg.Channels)
	if rc := d.lib.fnHWParamsSetChannels(p.pcm, hw, ch); rc < 0 {
		if rc2 := d.lib.fnHWParamsSetChannelsNear(p.pcm, hw, &ch); rc2 < 0 {
			return negotiatedHW{}, fmt.Errorf("set_channels: %w", d.errStr(rc2))
		}
	}
	rate := uint32(cfg.Rate)
	var dir int32
	if rc := d.lib.fnHWParamsSetRateNear(p.pcm, hw, &rate, &dir); rc < 0 {
		return negotiatedHW{}, fmt.Errorf("set_rate: %w", d.errStr(rc))
	}

	bufUS := cfg.BufferUS
	perUS := cfg.PeriodUS
	dir = 0
	_ = d.lib.fnHWParamsSetPeriodTimeNear(p.pcm, hw, &perUS, &dir)
	dir = 0
	_ = d.lib.fnHWParamsSetBufferTimeNear(p.pcm, hw, &bufUS, &dir)

	if rc := d.lib.fnHWParams(p.pcm, hw); rc < 0 {
		return negotiatedHW{}, fmt.Errorf("snd_pcm_hw_params: %w", d.errStr(rc))
	}

	var pFrames, bFrames uint64
	dir = 0
	_ = d.lib.fnHWParamsGetPeriodSize(hw, &pFrames, &dir)
	_ = d.lib.fnHWParamsGetBufferSize(hw, &bFrames)

	var sw uintptr
	if rc := d.lib.fnSWParamsMalloc(&sw); rc < 0 {
		return negotiatedHW{}, d.errStr(rc)
	}
	defer d.lib.fnSWParamsFree(sw)

	if rc := d.lib.fnSWParamsCurrent(p.pcm, sw); rc < 0 {
		return negotiatedHW{}, fmt.Errorf("sw_params_current: %w", d.errStr(rc))
	}
	_ = d.lib.fnSWParamsSetTstampMode(p.pcm, sw, TstampEnable)
	_ = d.lib.fnSWParamsSetTstampType(p.pcm, sw, TstampTypeMonotonic)
	if isPlayback && bFrames > 0 {
		_ = d.lib.fnSWParamsSetStartThreshold(p.pcm, sw, bFrames)
	}
	if rc := d.lib.fnSWParams(p.pcm, sw); rc < 0 {
		return negotiatedHW{}, fmt.Errorf("snd_pcm_sw_params: %w", d.errStr(rc))
	}

	return negotiatedHW{
		Format:       chosenFmt,
		Rate:         int(rate),
		Channels:     int(ch),
		PeriodFrames: int(pFrames),
		BufferFrames: int(bFrames),
	}, nil
}

func (d *puregoDriver) Prepare(h pcmHandle) error {
	if rc := d.lib.fnPrepare(h.(*puregoPCM).pcm); rc < 0 {
		return d.errStr(rc)
	}
	return nil
}

func (d *puregoDriver) Start(h pcmHandle) error {
	if rc := d.lib.fnStart(h.(*puregoPCM).pcm); rc < 0 {
		return d.errStr(rc)
	}
	return nil
}

func (d *puregoDriver) Wait(h pcmHandle, timeoutMS int) (bool, error) {
	rc := d.lib.fnWait(h.(*puregoPCM).pcm, int32(timeoutMS))
	if rc < 0 {
		if rc == ErrPipe {
			// Let the read/write loop recover on the next call.
			return true, nil
		}
		return false, d.errStr(rc)
	}
	return rc > 0, nil
}

func (d *puregoDriver) Readi(h pcmHandle, buf []byte, frames uint64) (int64, int32, error) {
	if len(buf) == 0 {
		return 0, 0, nil
	}
	n := d.lib.fnReadi(h.(*puregoPCM).pcm, unsafe.Pointer(&buf[0]), frames)
	runtime.KeepAlive(buf)
	if n < 0 {
		return 0, int32(n), d.errStr(int32(n))
	}
	return n, 0, nil
}

func (d *puregoDriver) Writei(h pcmHandle, buf []byte, frames uint64) (int64, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	p := h.(*puregoPCM).pcm
	n := d.lib.fnWritei(p, unsafe.Pointer(&buf[0]), frames)
	runtime.KeepAlive(buf)
	if n == int64(ErrPipe) {
		if rc := d.lib.fnRecover(p, int32(n), 1); rc < 0 {
			return 0, d.errStr(rc)
		}
		n = d.lib.fnWritei(p, unsafe.Pointer(&buf[0]), frames)
		runtime.KeepAlive(buf)
	}
	if n < 0 {
		return 0, d.errStr(int32(n))
	}
	return n, nil
}

func (d *puregoDriver) Recover(h pcmHandle, errCode int32) error {
	rc := d.lib.fnRecover(h.(*puregoPCM).pcm, errCode, 1)
	if rc < 0 {
		return d.errStr(rc)
	}
	return nil
}

func (d *puregoDriver) Status(h pcmHandle) (Timespec, uint64, int64, error) {
	p := h.(*puregoPCM)
	if rc := d.lib.fnStatus(p.pcm, p.status); rc < 0 {
		return Timespec{}, 0, 0, d.errStr(rc)
	}
	var ts Timespec
	d.lib.fnStatusGetHtstamp(p.status, &ts)
	avail := d.lib.fnStatusGetAvail(p.status)
	delay := d.lib.fnStatusGetDelay(p.status)
	return ts, avail, delay, nil
}
