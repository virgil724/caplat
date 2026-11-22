package wayland

import (
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/virgil724/caplat/displays"
)

func TestWireCodecAndHeader(t *testing.T) {
	var e Encoder
	e.Msg(10, 3, func(enc *Encoder) {
		enc.Uint32(42)
		enc.String("wl_shm")
		enc.Int32(-7)
		enc.FD(99)
	})
	b, fds := e.Bytes()
	// Header(8) + uint32(4) + strLen(4) + "wl_shm\0"(7 + 1 pad = 8) + int32(4) = 28 bytes.
	if len(b) != 28 {
		t.Fatalf("encoded len = %d, want 28", len(b))
	}
	if len(fds) != 1 || fds[0] != 99 {
		t.Fatalf("fds = %v, want [99]", fds)
	}
	obj, op, sz, err := ParseHeader(b)
	if err != nil || obj != 10 || op != 3 || sz != 28 {
		t.Fatalf("ParseHeader = (%d, %d, %d, %v)", obj, op, sz, err)
	}
	dec := NewDecoder(b[8:])
	u, err := dec.Uint32()
	if err != nil || u != 42 {
		t.Errorf("Uint32 = (%d, %v)", u, err)
	}
	s, err := dec.String()
	if err != nil || s != "wl_shm" {
		t.Errorf("String = (%q, %v)", s, err)
	}
	i, err := dec.Int32()
	if err != nil || i != -7 || dec.Remaining() != 0 {
		t.Errorf("Int32 = (%d, %v) rem=%d", i, err, dec.Remaining())
	}
}

type fakeSHM struct {
	mu     sync.Mutex
	nextFD int
	bufs   map[int][]byte
	closed map[int]int
}

func newFakeSHM() *fakeSHM {
	return &fakeSHM{
		nextFD: 100,
		bufs:   make(map[int][]byte),
		closed: make(map[int]int),
	}
}

func (s *fakeSHM) Alloc(_ string, size int) (int, []byte, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fd := s.nextFD
	s.nextFD++
	mem := make([]byte, size)
	s.bufs[fd] = mem
	return fd, mem, func() {}, nil
}

func (s *fakeSHM) CloseFD(fd int) {
	s.mu.Lock()
	s.closed[fd]++
	s.mu.Unlock()
}

type mockEvent struct {
	data []byte
	fds  []int
}

type mockCompositorTransport struct {
	mu sync.Mutex

	rxQueue []mockEvent
	rxWake  chan struct{}
	closed  bool

	// Object IDs tracked as client binds/creates them.
	registryID    uint32
	compositorID  uint32
	shmID         uint32
	xdgWmBaseID   uint32
	wpPresID      uint32
	seatID        uint32
	output1ID     uint32
	output2ID     uint32
	surfaceID     uint32
	xdgSurfaceID  uint32
	xdgToplevelID uint32
	pointerID     uint32
	keyboardID    uint32
	shmPoolID     uint32
	buffers       []uint32
	destroyedIDs  []uint32

	// Configurable behaviors.
	omitPresentation bool
	clockID          uint32
	feedbackFlags    uint32
	sendPongPing     bool
	abortAction      string // "esc", "click", "close", "protocol_err"
	abortOnFrame     int
	frameCount       int
	ackCount         int
	pongCount        int
	fullscreenOut    uint32
	sentFDs          []int
}

func newMockCompositor() *mockCompositorTransport {
	return &mockCompositorTransport{
		rxWake:        make(chan struct{}, 64),
		clockID:       clockMonotonic,
		feedbackFlags: FeedbackVsync | FeedbackHWClock | FeedbackZeroCopy,
		sendPongPing:  true,
	}
}

func (m *mockCompositorTransport) pushEvent(obj uint32, op uint16, fn func(*Encoder), fds ...int) {
	var e Encoder
	e.Msg(obj, op, fn)
	b, _ := e.Bytes()
	cp := append([]byte(nil), b...)
	m.rxQueue = append(m.rxQueue, mockEvent{data: cp, fds: fds})
	select {
	case m.rxWake <- struct{}{}:
	default:
	}
}

func (m *mockCompositorTransport) Send(data []byte, fds []int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return io.ErrClosedPipe
	}
	m.sentFDs = append(m.sentFDs, fds...)
	off := 0
	for off < len(data) {
		obj, op, sz, err := ParseHeader(data[off:])
		if err != nil {
			return err
		}
		dec := NewDecoder(data[off+8 : off+sz])
		off += sz
		m.handleReqLocked(obj, op, dec)
	}
	return nil
}

func (m *mockCompositorTransport) handleReqLocked(obj uint32, op uint16, dec *Decoder) {
	switch obj {
	case wlDisplayID:
		switch op {
		case wlDisplayReqGetRegistry:
			m.registryID, _ = dec.Uint32()
			m.pushEvent(m.registryID, wlRegistryEvGlobal, func(e *Encoder) {
				e.Uint32(1); e.String("wl_compositor"); e.Uint32(4)
			})
			m.pushEvent(m.registryID, wlRegistryEvGlobal, func(e *Encoder) {
				e.Uint32(2); e.String("wl_shm"); e.Uint32(1)
			})
			m.pushEvent(m.registryID, wlRegistryEvGlobal, func(e *Encoder) {
				e.Uint32(3); e.String("xdg_wm_base"); e.Uint32(2)
			})
			if !m.omitPresentation {
				m.pushEvent(m.registryID, wlRegistryEvGlobal, func(e *Encoder) {
					e.Uint32(4); e.String("wp_presentation"); e.Uint32(1)
				})
			}
			m.pushEvent(m.registryID, wlRegistryEvGlobal, func(e *Encoder) {
				e.Uint32(5); e.String("wl_seat"); e.Uint32(5)
			})
			m.pushEvent(m.registryID, wlRegistryEvGlobal, func(e *Encoder) {
				e.Uint32(6); e.String("wl_output"); e.Uint32(4)
			})
			m.pushEvent(m.registryID, wlRegistryEvGlobal, func(e *Encoder) {
				e.Uint32(7); e.String("wl_output"); e.Uint32(4)
			})
		case wlDisplayReqSync:
			cb, _ := dec.Uint32()
			m.pushEvent(cb, wlCallbackEvDone, func(e *Encoder) { e.Uint32(1) })
			m.pushEvent(wlDisplayID, wlDisplayEvDeleteID, func(e *Encoder) { e.Uint32(cb) })
		}

	case m.registryID:
		if op == wlRegistryReqBind {
			name, _ := dec.Uint32()
			iface, _ := dec.String()
			_, _ = dec.Uint32()
			id, _ := dec.Uint32()
			switch iface {
			case "wl_compositor":
				m.compositorID = id
			case "wl_shm":
				m.shmID = id
			case "xdg_wm_base":
				m.xdgWmBaseID = id
				if m.sendPongPing {
					m.pushEvent(m.xdgWmBaseID, xdgWmBaseEvPing, func(e *Encoder) { e.Uint32(99) })
				}
			case "wp_presentation":
				m.wpPresID = id
				m.pushEvent(m.wpPresID, wpPresentationEvClockID, func(e *Encoder) { e.Uint32(m.clockID) })
			case "wl_seat":
				m.seatID = id
				m.pushEvent(m.seatID, wlSeatEvCapabilities, func(e *Encoder) { e.Uint32(wlSeatCapPointer | wlSeatCapKeyboard) })
			case "wl_output":
				if name == 6 {
					m.output1ID = id
					m.pushEvent(id, wlOutputEvName, func(e *Encoder) { e.String("DP-1") })
					m.pushEvent(id, wlOutputEvMode, func(e *Encoder) {
						e.Uint32(wlOutputModeCurrent); e.Int32(2560); e.Int32(1440); e.Int32(144000)
					})
					m.pushEvent(id, wlOutputEvDone, nil)
				} else if name == 7 {
					m.output2ID = id
					m.pushEvent(id, wlOutputEvName, func(e *Encoder) { e.String("HDMI-A-1") })
					m.pushEvent(id, wlOutputEvDescription, func(e *Encoder) { e.String("AVT GC553") })
					m.pushEvent(id, wlOutputEvMode, func(e *Encoder) {
						e.Uint32(wlOutputModeCurrent); e.Int32(1920); e.Int32(1080); e.Int32(60000)
					})
					m.pushEvent(id, wlOutputEvDone, nil)
				}
			}
		}

	case m.seatID:
		switch op {
		case wlSeatReqGetPointer:
			m.pointerID, _ = dec.Uint32()
		case wlSeatReqGetKeyboard:
			m.keyboardID, _ = dec.Uint32()
			// Compositor always sends keymap FD on keyboard bind.
			m.pushEvent(m.keyboardID, wlKeyboardEvKeymap, func(e *Encoder) {
				e.Uint32(1); e.Uint32(4096)
			}, 777)
		}

	case m.compositorID:
		if op == wlCompositorReqCreateSurface {
			m.surfaceID, _ = dec.Uint32()
		}

	case m.xdgWmBaseID:
		switch op {
		case xdgWmBaseReqGetXdgSurface:
			m.xdgSurfaceID, _ = dec.Uint32()
		case xdgWmBaseReqPong:
			m.pongCount++
		}

	case m.xdgSurfaceID:
		switch op {
		case xdgSurfaceReqGetToplevel:
			m.xdgToplevelID, _ = dec.Uint32()
		case xdgSurfaceReqAckConfigure:
			m.ackCount++
		}

	case m.xdgToplevelID:
		if op == xdgToplevelReqSetFullscreen {
			m.fullscreenOut, _ = dec.Uint32()
		}

	case m.shmID:
		if op == wlShmReqCreatePool {
			m.shmPoolID, _ = dec.Uint32()
		}

	case m.shmPoolID:
		if op == wlShmPoolReqCreateBuffer {
			bid, _ := dec.Uint32()
			m.buffers = append(m.buffers, bid)
		}

	case m.wpPresID:
		if op == wpPresentationReqFeedback {
			_, _ = dec.Uint32()
			fbID, _ := dec.Uint32()
			m.frameCount++
			if m.abortOnFrame > 0 && m.frameCount == m.abortOnFrame {
				switch m.abortAction {
				case "esc":
					m.pushEvent(m.keyboardID, wlKeyboardEvKey, func(e *Encoder) {
						e.Uint32(1); e.Uint32(100); e.Uint32(linuxKeyEsc); e.Uint32(wlInputStatePressed)
					})
					return
				case "click":
					m.pushEvent(m.pointerID, wlPointerEvButton, func(e *Encoder) {
						e.Uint32(2); e.Uint32(100); e.Uint32(0x110); e.Uint32(wlInputStatePressed)
					})
					return
				case "close":
					m.pushEvent(m.xdgToplevelID, xdgToplevelEvClose, nil)
					return
				case "protocol_err":
					m.pushEvent(wlDisplayID, wlDisplayEvError, func(e *Encoder) {
						e.Uint32(m.surfaceID); e.Uint32(7); e.String("bad buffer")
					})
					return
				}
			}
			m.pushEvent(fbID, wpFeedbackEvSyncOutput, func(e *Encoder) { e.Uint32(m.output2ID) })
			tsNS := uint64(1_000_000_000_000 + uint64(m.frameCount)*16_666_667)
			sec := tsNS / 1_000_000_000
			nsec := uint32(tsNS % 1_000_000_000)
			m.pushEvent(fbID, wpFeedbackEvPresented, func(e *Encoder) {
				e.Uint32(uint32(sec >> 32))
				e.Uint32(uint32(sec))
				e.Uint32(nsec)
				e.Uint32(16_666_667)
				e.Uint32(0)
				e.Uint32(uint32(m.frameCount))
				e.Uint32(m.feedbackFlags)
			})
			m.pushEvent(wlDisplayID, wlDisplayEvDeleteID, func(e *Encoder) { e.Uint32(fbID) })
		}

	case m.surfaceID:
		switch op {
		case wlSurfaceReqAttach:
			bid, _ := dec.Uint32()
			// Release older buffer on commit; track latest attached.
			if len(m.buffers) > 0 && bid != 0 {
				m.pushEvent(bid, wlBufferEvRelease, nil)
			}
		case wlSurfaceReqFrame:
			cb, _ := dec.Uint32()
			m.pushEvent(cb, wlCallbackEvDone, func(e *Encoder) { e.Uint32(uint32(m.frameCount)) })
			m.pushEvent(wlDisplayID, wlDisplayEvDeleteID, func(e *Encoder) { e.Uint32(cb) })
		case wlSurfaceReqCommit:
			if m.ackCount == 0 && m.xdgSurfaceID != 0 {
				m.pushEvent(m.xdgToplevelID, xdgToplevelEvConfigure, func(e *Encoder) {
					e.Int32(1920); e.Int32(1080)
					e.Uint32(0) // empty states array
				})
				m.pushEvent(m.xdgSurfaceID, xdgSurfaceEvConfigure, func(e *Encoder) {
					e.Uint32(555)
				})
			}
		}
	}

	if op == xdgToplevelReqDestroy || op == xdgSurfaceReqDestroy || op == wlSurfaceReqDestroy {
		m.destroyedIDs = append(m.destroyedIDs, obj)
	}
}

func (m *mockCompositorTransport) Recv(buf []byte, timeoutMS int) (int, []int, error) {
	m.mu.Lock()
	if len(m.rxQueue) > 0 {
		ev := m.rxQueue[0]
		m.rxQueue = m.rxQueue[1:]
		n := copy(buf, ev.data)
		if n < len(ev.data) {
			rem := mockEvent{data: append([]byte(nil), ev.data[n:]...)}
			m.rxQueue = append([]mockEvent{rem}, m.rxQueue...)
		}
		m.mu.Unlock()
		return n, ev.fds, nil
	}
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return 0, nil, io.EOF
	}
	if timeoutMS == 0 {
		return 0, nil, nil
	}
	select {
	case <-m.rxWake:
		return m.Recv(buf, 0)
	default:
		return 0, nil, nil
	}
}

func (m *mockCompositorTransport) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	return nil
}

func TestWaylandClientHappyPath(t *testing.T) {
	comp := newMockCompositor()
	shm := newFakeSHM()
	var warnings []string
	c := NewClient(comp, shm, func(msg string) { warnings = append(warnings, msg) })

	var vblanks []struct {
		frame uint64
		ns    int64
	}
	d := displays.Display{Name: "HDMI-A-1", FriendlyName: "AVT GC553", Width: 1920, Height: 1080}
	if err := c.Open(d, func(frame uint64, ns int64) {
		vblanks = append(vblanks, struct {
			frame uint64
			ns    int64
		}{frame, ns})
	}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if comp.fullscreenOut != comp.output2ID {
		t.Errorf("set_fullscreen output = %d, want HDMI-A-1 id %d", comp.fullscreenOut, comp.output2ID)
	}
	if comp.pongCount != 1 || comp.ackCount != 1 {
		t.Errorf("pong=%d ack=%d, want 1/1", comp.pongCount, comp.ackCount)
	}
	if shm.closed[777] != 1 {
		t.Errorf("wl_keyboard.keymap fd 777 closed %d times, want 1", shm.closed[777])
	}
	if len(comp.sentFDs) != 1 || shm.closed[comp.sentFDs[0]] != 1 {
		t.Errorf("shm pool fd lifecycle mismatch: sent=%v closed=%v", comp.sentFDs, shm.closed)
	}

	// Frame 0 is warm-up (should not emit onVblank); frames 1..5 should emit.
	for f := uint64(0); f <= 5; f++ {
		if err := c.Present(f, func(buf []byte, w, stride int) {
			if len(buf) < w*4 || stride != w*4 {
				t.Errorf("unexpected draw geometry len=%d w=%d stride=%d", len(buf), w, stride)
			}
			buf[0] = byte(f)
		}); err != nil {
			t.Fatalf("Present(%d): %v", f, err)
		}
	}
	if len(vblanks) != 5 {
		t.Fatalf("got %d vblanks, want 5", len(vblanks))
	}
	if vblanks[0].frame != 1 || vblanks[4].frame != 5 {
		t.Errorf("vblank frames = %+v", vblanks)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings with ZERO_COPY enabled: %v", warnings)
	}
	if len(c.freeIDs) == 0 {
		t.Errorf("expected delete_id to recycle callback/feedback object IDs")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(comp.destroyedIDs) < 3 {
		t.Errorf("expected cleanup destroy calls, got %v", comp.destroyedIDs)
	}
}

func TestWaylandClientErrorsAndAborts(t *testing.T) {
	t.Run("missing_wp_presentation", func(t *testing.T) {
		comp := newMockCompositor()
		comp.omitPresentation = true
		c := NewClient(comp, newFakeSHM(), nil)
		err := c.Open(displays.Display{Name: "HDMI-A-1", Width: 1920, Height: 1080}, nil)
		if err == nil || !strings.Contains(err.Error(), "wp_presentation") {
			t.Fatalf("got %v", err)
		}
		_ = c.Close()
	})

	t.Run("non_monotonic_clock", func(t *testing.T) {
		comp := newMockCompositor()
		comp.clockID = 0 // CLOCK_REALTIME
		c := NewClient(comp, newFakeSHM(), nil)
		err := c.Open(displays.Display{Name: "HDMI-A-1", Width: 1920, Height: 1080}, nil)
		if err == nil || !strings.Contains(err.Error(), "CLOCK_MONOTONIC") {
			t.Fatalf("got %v", err)
		}
		_ = c.Close()
	})

	t.Run("missing_hw_clock_flag", func(t *testing.T) {
		comp := newMockCompositor()
		comp.feedbackFlags = FeedbackVsync
		c := NewClient(comp, newFakeSHM(), nil)
		if err := c.Open(displays.Display{Name: "HDMI-A-1", Width: 1920, Height: 1080}, nil); err != nil {
			t.Fatal(err)
		}
		err := c.Present(1, func([]byte, int, int) {})
		if err == nil || !strings.Contains(err.Error(), "HW_CLOCK") {
			t.Fatalf("got %v", err)
		}
		_ = c.Close()
	})

	t.Run("missing_zero_copy_warns_once", func(t *testing.T) {
		comp := newMockCompositor()
		comp.feedbackFlags = FeedbackVsync | FeedbackHWClock
		var warns []string
		c := NewClient(comp, newFakeSHM(), func(s string) { warns = append(warns, s) })
		if err := c.Open(displays.Display{Name: "HDMI-A-1", Width: 1920, Height: 1080}, nil); err != nil {
			t.Fatal(err)
		}
		_ = c.Present(1, func([]byte, int, int) {})
		_ = c.Present(2, func([]byte, int, int) {})
		if len(warns) != 1 || !strings.Contains(warns[0], "ZERO_COPY") {
			t.Errorf("warnings = %v, want 1 ZERO_COPY warning", warns)
		}
		_ = c.Close()
	})

	for _, action := range []string{"esc", "click", "close"} {
		t.Run("abort_"+action, func(t *testing.T) {
			comp := newMockCompositor()
			comp.abortAction = action
			comp.abortOnFrame = 2
			c := NewClient(comp, newFakeSHM(), nil)
			if err := c.Open(displays.Display{Name: "HDMI-A-1", Width: 1920, Height: 1080}, nil); err != nil {
				t.Fatal(err)
			}
			if err := c.Present(1, func([]byte, int, int) {}); err != nil {
				t.Fatalf("frame 1: %v", err)
			}
			err := c.Present(2, func([]byte, int, int) {})
			if !errors.Is(err, ErrAborted) {
				t.Fatalf("frame 2 got %v, want ErrAborted", err)
			}
			_ = c.Close()
		})
	}

	t.Run("protocol_error", func(t *testing.T) {
		comp := newMockCompositor()
		comp.abortAction = "protocol_err"
		comp.abortOnFrame = 1
		c := NewClient(comp, newFakeSHM(), nil)
		if err := c.Open(displays.Display{Name: "HDMI-A-1", Width: 1920, Height: 1080}, nil); err != nil {
			t.Fatal(err)
		}
		err := c.Present(1, func([]byte, int, int) {})
		if err == nil || !strings.Contains(err.Error(), "bad buffer") {
			t.Fatalf("got %v", err)
		}
		_ = c.Close()
	})
}
