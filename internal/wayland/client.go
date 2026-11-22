package wayland

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/virgil724/caplat/displays"
)

// ErrAborted is returned when the user presses Esc, clicks the measurement
// surface, or the compositor closes the toplevel.
var ErrAborted = errors.New("aborted from the measurement window (Esc or click)")

// Transport abstracts a Wayland stream socket capable of passing FDs via SCM_RIGHTS.
type Transport interface {
	Send(data []byte, fds []int) error
	Recv(buf []byte, timeoutMS int) (n int, fds []int, err error)
	Close() error
}

// SHMAllocator allocates shared memory buffers backed by an FD suitable for
// passing to wl_shm.create_pool.
type SHMAllocator interface {
	Alloc(name string, size int) (fd int, data []byte, cleanup func(), err error)
	CloseFD(fd int)
}

type globalInfo struct {
	name    uint32
	iface   string
	version uint32
}

type wlOutputState struct {
	globalName uint32
	id         uint32
	ver        uint32
	x, y       int32
	make       string
	model      string
	name       string
	desc       string
	width      int
	height     int
	scale      int32
}

type shmBuf struct {
	id     uint32
	offset int
	data   []byte
	busy   bool
}

// Client is a minimal, pure-Go Wayland client implementing wl_shm triple-buffering,
// xdg-shell fullscreen output targeting, and wp_presentation hardware vblank
// timestamps.
type Client struct {
	tr       Transport
	shm      SHMAllocator
	warn     func(string)
	onVblank func(frame uint64, ns int64)

	enc     Encoder
	nextID  uint32
	freeIDs []uint32

	rxBuf   [8192]byte
	rxAcc   []byte
	fdQueue []int

	globals         map[uint32]globalInfo
	registryID      uint32
	compositorID    uint32
	compositorVer   uint32
	shmID           uint32
	xdgWmBaseID     uint32
	xdgWmBaseVer    uint32
	wpPresID        uint32
	seatID          uint32
	seatVer         uint32
	pointerID       uint32
	keyboardID      uint32

	outputs map[uint32]*wlOutputState

	clockID    uint32
	hasClockID bool

	surfaceID     uint32
	regionID      uint32
	xdgSurfaceID  uint32
	xdgToplevelID uint32
	shmPoolID     uint32
	shmCleanup    func()

	configured   bool
	configWidth  int
	configHeight int
	targetOutput *wlOutputState

	bufW, bufH, bufStride int
	bufs                  []shmBuf
	firstCommit           bool

	pendingCallbacks map[uint32]bool
	callbacksDone    map[uint32]bool
	pendingFB        map[uint32]uint64 // wp_presentation_feedback ID -> frame number

	aborted        bool
	warnedZeroCopy bool
	fatalErr       error
	closed         bool
}

// NewClient constructs a Client over tr and shm. If warn is nil, warnings are
// written to os.Stderr.
func NewClient(tr Transport, shm SHMAllocator, warn func(string)) *Client {
	if warn == nil {
		warn = func(msg string) { fmt.Fprintln(os.Stderr, msg) }
	}
	return &Client{
		tr:               tr,
		shm:              shm,
		warn:             warn,
		nextID:           2, // 1 is wl_display
		globals:          make(map[uint32]globalInfo),
		outputs:          make(map[uint32]*wlOutputState),
		pendingCallbacks: make(map[uint32]bool),
		callbacksDone:    make(map[uint32]bool),
		pendingFB:        make(map[uint32]uint64),
	}
}

func (c *Client) allocID() uint32 {
	if n := len(c.freeIDs); n > 0 {
		id := c.freeIDs[n-1]
		c.freeIDs = c.freeIDs[:n-1]
		return id
	}
	id := c.nextID
	c.nextID++
	return id
}

func (c *Client) flush() error {
	data, fds := c.enc.Bytes()
	if len(data) == 0 && len(fds) == 0 {
		return nil
	}
	err := c.tr.Send(data, fds)
	c.enc.Reset()
	return err
}

func (c *Client) roundtrip() error {
	cb := c.allocID()
	c.pendingCallbacks[cb] = true
	c.enc.Msg(wlDisplayID, wlDisplayReqSync, func(e *Encoder) {
		e.Uint32(cb)
	})
	if err := c.flush(); err != nil {
		delete(c.pendingCallbacks, cb)
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for !c.callbacksDone[cb] {
		if c.fatalErr != nil {
			delete(c.pendingCallbacks, cb)
			return c.fatalErr
		}
		if time.Now().After(deadline) {
			delete(c.pendingCallbacks, cb)
			return errors.New("wayland: compositor sync roundtrip timed out")
		}
		if err := c.dispatch(200); err != nil {
			delete(c.pendingCallbacks, cb)
			return err
		}
	}
	delete(c.pendingCallbacks, cb)
	delete(c.callbacksDone, cb)
	return nil
}

// Open binds Wayland globals, verifies wp_presentation CLOCK_MONOTONIC, creates
// a fullscreen xdg_toplevel on the output matching d, and allocates 3 wl_shm buffers.
func (c *Client) Open(d displays.Display, onVblank func(frame uint64, ns int64)) error {
	c.onVblank = onVblank

	c.registryID = c.allocID()
	c.enc.Msg(wlDisplayID, wlDisplayReqGetRegistry, func(e *Encoder) {
		e.Uint32(c.registryID)
	})
	if err := c.roundtrip(); err != nil {
		return err
	}

	var compName, shmName, wmName, presName, seatName *globalInfo
	var outGlobals []globalInfo
	for _, g := range c.globals {
		g := g
		switch g.iface {
		case "wl_compositor":
			compName = &g
		case "wl_shm":
			shmName = &g
		case "xdg_wm_base":
			wmName = &g
		case "wp_presentation":
			presName = &g
		case "wl_seat":
			seatName = &g
		case "wl_output":
			outGlobals = append(outGlobals, g)
		}
	}
	if compName == nil {
		return errors.New("wayland: compositor missing wl_compositor")
	}
	if shmName == nil {
		return errors.New("wayland: compositor missing wl_shm")
	}
	if wmName == nil {
		return errors.New("wayland: compositor missing xdg_wm_base")
	}
	if presName == nil {
		return errors.New("wayland: compositor missing wp_presentation (required for vblank timestamps)")
	}

	c.compositorVer = min(compName.version, 4)
	c.compositorID = c.bindGlobal(c.registryID, compName.name, "wl_compositor", c.compositorVer)
	c.shmID = c.bindGlobal(c.registryID, shmName.name, "wl_shm", 1)
	c.xdgWmBaseVer = min(wmName.version, 2)
	c.xdgWmBaseID = c.bindGlobal(c.registryID, wmName.name, "xdg_wm_base", c.xdgWmBaseVer)
	c.wpPresID = c.bindGlobal(c.registryID, presName.name, "wp_presentation", 1)
	if seatName != nil {
		c.seatVer = min(seatName.version, 5)
		c.seatID = c.bindGlobal(c.registryID, seatName.name, "wl_seat", c.seatVer)
	}
	for _, og := range outGlobals {
		ver := min(og.version, 4)
		oid := c.bindGlobal(c.registryID, og.name, "wl_output", ver)
		c.outputs[oid] = &wlOutputState{globalName: og.name, id: oid, ver: ver, scale: 1}
	}

	if err := c.roundtrip(); err != nil {
		return err
	}
	if !c.hasClockID {
		return errors.New("wayland: wp_presentation did not advertise clock_id")
	}
	if c.clockID != clockMonotonic {
		return fmt.Errorf("wayland: wp_presentation clock_id=%d, want CLOCK_MONOTONIC (%d)", c.clockID, clockMonotonic)
	}

	c.targetOutput = c.matchOutput(d)
	if c.targetOutput == nil && len(c.outputs) > 0 && d.Name != "" {
		return fmt.Errorf("wayland: output %q not found among compositor wl_outputs", d.Name)
	}

	c.surfaceID = c.allocID()
	c.enc.Msg(c.compositorID, wlCompositorReqCreateSurface, func(e *Encoder) {
		e.Uint32(c.surfaceID)
	})
	c.xdgSurfaceID = c.allocID()
	c.enc.Msg(c.xdgWmBaseID, xdgWmBaseReqGetXdgSurface, func(e *Encoder) {
		e.Uint32(c.xdgSurfaceID)
		e.Uint32(c.surfaceID)
	})
	c.xdgToplevelID = c.allocID()
	c.enc.Msg(c.xdgSurfaceID, xdgSurfaceReqGetToplevel, func(e *Encoder) {
		e.Uint32(c.xdgToplevelID)
	})
	c.enc.Msg(c.xdgToplevelID, xdgToplevelReqSetTitle, func(e *Encoder) {
		e.String("caplat")
	})
	c.enc.Msg(c.xdgToplevelID, xdgToplevelReqSetAppID, func(e *Encoder) {
		e.String("caplat")
	})
	outObjID := uint32(0)
	if c.targetOutput != nil {
		outObjID = c.targetOutput.id
	}
	c.enc.Msg(c.xdgToplevelID, xdgToplevelReqSetFullscreen, func(e *Encoder) {
		e.Uint32(outObjID)
	})
	c.enc.Msg(c.surfaceID, wlSurfaceReqCommit, nil)
	if err := c.flush(); err != nil {
		return err
	}

	deadline := time.Now().Add(3 * time.Second)
	for !c.configured {
		if c.aborted {
			return ErrAborted
		}
		if c.fatalErr != nil {
			return c.fatalErr
		}
		if time.Now().After(deadline) {
			return errors.New("wayland: timed out waiting for initial xdg_surface.configure")
		}
		if err := c.dispatch(200); err != nil {
			return err
		}
	}

	w, h := c.resolveBufferSize(d)
	if w <= 0 || h <= 0 {
		return fmt.Errorf("wayland: invalid surface size %dx%d", w, h)
	}
	c.bufW, c.bufH, c.bufStride = w, h, w*4
	c.firstCommit = true

	// Declare the entire surface opaque so the compositor can promote it to
	// direct hardware scanout (ZERO_COPY).
	c.regionID = c.allocID()
	c.enc.Msg(c.compositorID, wlCompositorReqCreateRegion, func(e *Encoder) {
		e.Uint32(c.regionID)
	})
	c.enc.Msg(c.regionID, wlRegionReqAdd, func(e *Encoder) {
		e.Int32(0)
		e.Int32(0)
		e.Int32(int32(w))
		e.Int32(int32(h))
	})
	c.enc.Msg(c.surfaceID, wlSurfaceReqSetOpaqueRegion, func(e *Encoder) {
		e.Uint32(c.regionID)
	})

	if err := c.allocSHMBuffers(w, h, 3); err != nil {
		return err
	}
	return c.flush()
}

func (c *Client) bindGlobal(regID, name uint32, iface string, ver uint32) uint32 {
	id := c.allocID()
	c.enc.Msg(regID, wlRegistryReqBind, func(e *Encoder) {
		e.Uint32(name)
		e.String(iface)
		e.Uint32(ver)
		e.Uint32(id)
	})
	return id
}

func (c *Client) matchOutput(d displays.Display) *wlOutputState {
	for _, o := range c.outputs {
		if d.Name != "" && strings.EqualFold(o.name, d.Name) {
			return o
		}
	}
	if d.FriendlyName != "" {
		want := strings.ToLower(d.FriendlyName)
		for _, o := range c.outputs {
			if strings.Contains(strings.ToLower(o.desc), want) || strings.Contains(strings.ToLower(o.model), want) {
				return o
			}
		}
	}
	for _, o := range c.outputs {
		if d.Width > 0 && o.width == d.Width && o.height == d.Height &&
			(o.x == d.Rect.Left && o.y == d.Rect.Top || len(c.outputs) == 1) {
			return o
		}
	}
	return nil
}

func (c *Client) resolveBufferSize(d displays.Display) (w, h int) {
	if c.configWidth > 0 && c.configHeight > 0 {
		if c.targetOutput != nil && c.targetOutput.scale > 1 && c.compositorVer >= 3 &&
			c.configWidth*int(c.targetOutput.scale) == d.Width && c.configHeight*int(c.targetOutput.scale) == d.Height {
			c.enc.Msg(c.surfaceID, wlSurfaceReqSetBufferScale, func(e *Encoder) {
				e.Int32(c.targetOutput.scale)
			})
			return d.Width, d.Height
		}
		return c.configWidth, c.configHeight
	}
	if d.Width > 0 && d.Height > 0 {
		return d.Width, d.Height
	}
	if c.targetOutput != nil && c.targetOutput.width > 0 {
		return c.targetOutput.width, c.targetOutput.height
	}
	return 0, 0
}

func (c *Client) allocSHMBuffers(w, h, count int) error {
	frameSize := w * h * 4
	totalSize := frameSize * count
	fd, mem, cleanup, err := c.shm.Alloc("caplat-wl-shm", totalSize)
	if err != nil {
		return fmt.Errorf("wayland shm alloc (%d bytes): %w", totalSize, err)
	}
	c.shmCleanup = cleanup
	c.shmPoolID = c.allocID()
	c.enc.Msg(c.shmID, wlShmReqCreatePool, func(e *Encoder) {
		e.Uint32(c.shmPoolID)
		e.FD(fd)
		e.Int32(int32(totalSize))
	})
	if err := c.flush(); err != nil {
		c.shm.CloseFD(fd)
		return err
	}
	c.shm.CloseFD(fd)

	c.bufs = make([]shmBuf, count)
	for i := 0; i < count; i++ {
		off := i * frameSize
		bid := c.allocID()
		c.enc.Msg(c.shmPoolID, wlShmPoolReqCreateBuffer, func(e *Encoder) {
			e.Uint32(bid)
			e.Int32(int32(off))
			e.Int32(int32(w))
			e.Int32(int32(h))
			e.Int32(int32(w * 4))
			e.Uint32(wlShmFormatXRGB888)
		})
		c.bufs[i] = shmBuf{
			id:     bid,
			offset: off,
			data:   mem[off : off+frameSize],
		}
	}
	return nil
}

// BufferGeometry returns the negotiated SHM buffer dimensions and row stride.
func (c *Client) BufferGeometry() (w, h, stride int) {
	return c.bufW, c.bufH, c.bufStride
}

// Present draws one frame via draw, commits it with wp_presentation feedback,
// and waits for the compositor's wl_surface.frame callback before returning.
func (c *Client) Present(frame uint64, draw func(buf []byte, w, stride int)) error {
	if err := c.dispatchNonBlock(); err != nil {
		return err
	}
	if c.aborted {
		return ErrAborted
	}
	if c.fatalErr != nil {
		return c.fatalErr
	}

	bufIdx := c.findFreeBuffer()
	deadline := time.Now().Add(2 * time.Second)
	for bufIdx < 0 {
		if c.aborted {
			return ErrAborted
		}
		if c.fatalErr != nil {
			return c.fatalErr
		}
		if time.Now().After(deadline) {
			return errors.New("wayland: timed out waiting for compositor wl_buffer.release")
		}
		if err := c.dispatch(100); err != nil {
			return err
		}
		bufIdx = c.findFreeBuffer()
	}

	b := &c.bufs[bufIdx]
	draw(b.data, c.bufW, c.bufStride)
	b.busy = true

	c.enc.Msg(c.surfaceID, wlSurfaceReqAttach, func(e *Encoder) {
		e.Uint32(b.id)
		e.Int32(0)
		e.Int32(0)
	})
	damageH := int32(c.bufH)
	if !c.firstCommit {
		// Only the top barcode row changes after the first full black clear.
		damageH = min(int32(c.bufH), int32(max(c.bufW/32, 1)))
	}
	c.firstCommit = false
	if c.compositorVer >= 4 {
		c.enc.Msg(c.surfaceID, wlSurfaceReqDamageBuffer, func(e *Encoder) {
			e.Int32(0)
			e.Int32(0)
			e.Int32(int32(c.bufW))
			e.Int32(damageH)
		})
	} else {
		c.enc.Msg(c.surfaceID, wlSurfaceReqDamage, func(e *Encoder) {
			e.Int32(0)
			e.Int32(0)
			e.Int32(int32(c.bufW))
			e.Int32(damageH)
		})
	}

	fbID := c.allocID()
	c.pendingFB[fbID] = frame
	c.enc.Msg(c.wpPresID, wpPresentationReqFeedback, func(e *Encoder) {
		e.Uint32(c.surfaceID)
		e.Uint32(fbID)
	})

	frameCB := c.allocID()
	c.pendingCallbacks[frameCB] = true
	c.enc.Msg(c.surfaceID, wlSurfaceReqFrame, func(e *Encoder) {
		e.Uint32(frameCB)
	})
	c.enc.Msg(c.surfaceID, wlSurfaceReqCommit, nil)
	if err := c.flush(); err != nil {
		delete(c.pendingCallbacks, frameCB)
		return err
	}

	frameDeadline := time.Now().Add(2 * time.Second)
	for !c.callbacksDone[frameCB] {
		if c.aborted {
			delete(c.pendingCallbacks, frameCB)
			return ErrAborted
		}
		if c.fatalErr != nil {
			delete(c.pendingCallbacks, frameCB)
			return c.fatalErr
		}
		if time.Now().After(frameDeadline) {
			delete(c.pendingCallbacks, frameCB)
			return errors.New("wayland: wl_surface.frame callback timed out (is output unplugged or asleep?)")
		}
		if err := c.dispatch(100); err != nil {
			delete(c.pendingCallbacks, frameCB)
			return err
		}
	}
	delete(c.pendingCallbacks, frameCB)
	delete(c.callbacksDone, frameCB)
	if c.fatalErr != nil {
		return c.fatalErr
	}
	return nil
}

func (c *Client) findFreeBuffer() int {
	for i := range c.bufs {
		if !c.bufs[i].busy {
			return i
		}
	}
	return -1
}

func (c *Client) dispatchNonBlock() error {
	for {
		n, fds, err := c.tr.Recv(c.rxBuf[:], 0)
		if len(fds) > 0 {
			c.fdQueue = append(c.fdQueue, fds...)
		}
		if n > 0 {
			c.rxAcc = append(c.rxAcc, c.rxBuf[:n]...)
		}
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
	}
	return c.processMessages()
}

func (c *Client) dispatch(timeoutMS int) error {
	n, fds, err := c.tr.Recv(c.rxBuf[:], timeoutMS)
	if len(fds) > 0 {
		c.fdQueue = append(c.fdQueue, fds...)
	}
	if n > 0 {
		c.rxAcc = append(c.rxAcc, c.rxBuf[:n]...)
		if perr := c.processMessages(); perr != nil {
			return perr
		}
	}
	if err != nil {
		return err
	}
	return c.processMessages()
}

func (c *Client) processMessages() error {
	off := 0
	for len(c.rxAcc)-off >= 8 {
		objID, opcode, size, err := ParseHeader(c.rxAcc[off:])
		if err != nil {
			return err
		}
		if len(c.rxAcc)-off < size {
			break
		}
		payload := c.rxAcc[off+8 : off+size]
		off += size
		if err := c.handleEvent(objID, opcode, payload); err != nil {
			return err
		}
	}
	if off > 0 {
		rem := copy(c.rxAcc, c.rxAcc[off:])
		c.rxAcc = c.rxAcc[:rem]
	}
	return c.flush()
}

func (c *Client) popFD() (int, bool) {
	if len(c.fdQueue) == 0 {
		return -1, false
	}
	fd := c.fdQueue[0]
	c.fdQueue = c.fdQueue[1:]
	return fd, true
}

func (c *Client) handleEvent(objID uint32, opcode uint16, payload []byte) error {
	dec := NewDecoder(payload)
	switch objID {
	case wlDisplayID:
		switch opcode {
		case wlDisplayEvError:
			errObj, err := dec.Uint32()
			if err != nil {
				return err
			}
			code, err := dec.Uint32()
			if err != nil {
				return err
			}
			msg, err := dec.String()
			if err != nil {
				return err
			}
			c.fatalErr = fmt.Errorf("wayland protocol error on object %d (code %d): %s", errObj, code, msg)
			return c.fatalErr
		case wlDisplayEvDeleteID:
			id, err := dec.Uint32()
			if err != nil {
				return err
			}
			if id >= 2 {
				c.freeIDs = append(c.freeIDs, id)
			}
			return nil
		}
		return nil

	case c.wpPresID:
		if opcode == wpPresentationEvClockID {
			clk, err := dec.Uint32()
			if err != nil {
				return err
			}
			c.clockID = clk
			c.hasClockID = true
		}
		return nil

	case c.xdgWmBaseID:
		if opcode == xdgWmBaseEvPing {
			serial, err := dec.Uint32()
			if err != nil {
				return err
			}
			c.enc.Msg(c.xdgWmBaseID, xdgWmBaseReqPong, func(e *Encoder) {
				e.Uint32(serial)
			})
		}
		return nil

	case c.xdgSurfaceID:
		if opcode == xdgSurfaceEvConfigure {
			serial, err := dec.Uint32()
			if err != nil {
				return err
			}
			c.enc.Msg(c.xdgSurfaceID, xdgSurfaceReqAckConfigure, func(e *Encoder) {
				e.Uint32(serial)
			})
			c.configured = true
		}
		return nil

	case c.xdgToplevelID:
		switch opcode {
		case xdgToplevelEvConfigure:
			w, err := dec.Int32()
			if err != nil {
				return err
			}
			h, err := dec.Int32()
			if err != nil {
				return err
			}
			_, err = dec.Array()
			if err != nil {
				return err
			}
			if w > 0 && h > 0 {
				c.configWidth, c.configHeight = int(w), int(h)
			}
		case xdgToplevelEvClose:
			c.aborted = true
		}
		return nil

	case c.seatID:
		if opcode == wlSeatEvCapabilities {
			caps, err := dec.Uint32()
			if err != nil {
				return err
			}
			if caps&wlSeatCapPointer != 0 && c.pointerID == 0 {
				c.pointerID = c.allocID()
				c.enc.Msg(c.seatID, wlSeatReqGetPointer, func(e *Encoder) {
					e.Uint32(c.pointerID)
				})
			}
			if caps&wlSeatCapKeyboard != 0 && c.keyboardID == 0 {
				c.keyboardID = c.allocID()
				c.enc.Msg(c.seatID, wlSeatReqGetKeyboard, func(e *Encoder) {
					e.Uint32(c.keyboardID)
				})
			}
		}
		return nil

	case c.keyboardID:
		switch opcode {
		case wlKeyboardEvKeymap:
			// wl_keyboard.keymap carries an FD over SCM_RIGHTS that must be popped & closed.
			if fd, ok := c.popFD(); ok {
				c.shm.CloseFD(fd)
			}
		case wlKeyboardEvKey:
			_, _ = dec.Uint32() // serial
			_, _ = dec.Uint32() // time
			key, err := dec.Uint32()
			if err != nil {
				return err
			}
			state, err := dec.Uint32()
			if err != nil {
				return err
			}
			if key == linuxKeyEsc && state == wlInputStatePressed {
				c.aborted = true
			}
		}
		return nil

	case c.pointerID:
		if opcode == wlPointerEvButton {
			_, _ = dec.Uint32() // serial
			_, _ = dec.Uint32() // time
			_, _ = dec.Uint32() // button
			state, err := dec.Uint32()
			if err != nil {
				return err
			}
			if state == wlInputStatePressed {
				c.aborted = true
			}
		}
		return nil

	case c.shmID, c.surfaceID:
		return nil

	case c.registryID:
		switch opcode {
		case wlRegistryEvGlobal:
			name, err := dec.Uint32()
			if err != nil {
				return err
			}
			iface, err := dec.String()
			if err != nil {
				return err
			}
			ver, err := dec.Uint32()
			if err != nil {
				return err
			}
			c.globals[name] = globalInfo{name: name, iface: iface, version: ver}
		case wlRegistryEvGlobalRemove:
			name, err := dec.Uint32()
			if err != nil {
				return err
			}
			delete(c.globals, name)
		}
		return nil
	}

	// wl_output objects.
	if out, ok := c.outputs[objID]; ok {
		switch opcode {
		case wlOutputEvGeometry:
			x, _ := dec.Int32()
			y, _ := dec.Int32()
			_, _ = dec.Int32()
			_, _ = dec.Int32()
			_, _ = dec.Int32()
			mk, _ := dec.String()
			md, _ := dec.String()
			out.x, out.y, out.make, out.model = x, y, mk, md
		case wlOutputEvMode:
			flags, _ := dec.Uint32()
			w, _ := dec.Int32()
			h, _ := dec.Int32()
			if flags&wlOutputModeCurrent != 0 {
				out.width, out.height = int(w), int(h)
			}
		case wlOutputEvScale:
			if s, err := dec.Int32(); err == nil && s > 0 {
				out.scale = s
			}
		case wlOutputEvName:
			if name, err := dec.String(); err == nil {
				out.name = name
			}
		case wlOutputEvDescription:
			if desc, err := dec.String(); err == nil {
				out.desc = desc
			}
		}
		return nil
	}

	// wl_buffer.release
	for i := range c.bufs {
		if c.bufs[i].id == objID && opcode == wlBufferEvRelease {
			c.bufs[i].busy = false
			return nil
		}
	}

	// wp_presentation_feedback
	if frame, ok := c.pendingFB[objID]; ok {
		switch opcode {
		case wpFeedbackEvSyncOutput:
			return nil
		case wpFeedbackEvPresented:
			delete(c.pendingFB, objID)
			secHi, err := dec.Uint32()
			if err != nil {
				return err
			}
			secLo, err := dec.Uint32()
			if err != nil {
				return err
			}
			nsec, err := dec.Uint32()
			if err != nil {
				return err
			}
			_, _ = dec.Uint32() // refresh
			_, _ = dec.Uint32() // seq_hi
			_, _ = dec.Uint32() // seq_lo
			flags, err := dec.Uint32()
			if err != nil {
				return err
			}
			if flags&FeedbackHWClock == 0 {
				c.fatalErr = errors.New("wayland: wp_presentation_feedback missing HW_CLOCK flag (compositor vblank timestamp is not a hardware clock)")
				return c.fatalErr
			}
			if flags&FeedbackZeroCopy == 0 && !c.warnedZeroCopy {
				c.warnedZeroCopy = true
				c.warn("caplat: warning: Wayland compositor did not report ZERO_COPY direct scanout; latency includes compositor compositing")
			}
			sec := (uint64(secHi) << 32) | uint64(secLo)
			ns := int64(sec)*1_000_000_000 + int64(nsec)
			if frame != 0 && c.onVblank != nil {
				c.onVblank(frame, ns)
			}
			return nil
		case wpFeedbackEvDiscarded:
			delete(c.pendingFB, objID)
			return nil
		}
	}

	// wl_callback.done
	if opcode == wlCallbackEvDone && c.pendingCallbacks[objID] {
		c.callbacksDone[objID] = true
		return nil
	}
	return nil
}

// Close destroys Wayland protocol objects, unmaps SHM buffers, and closes the socket.
func (c *Client) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true

	// Briefly drain any final pending wp_presentation_feedback events.
	if !c.aborted && c.fatalErr == nil && c.tr != nil {
		deadline := time.Now().Add(40 * time.Millisecond)
		for len(c.pendingFB) > 0 && time.Now().Before(deadline) {
			if err := c.dispatch(20); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				break
			}
		}
	}

	for _, fd := range c.fdQueue {
		c.shm.CloseFD(fd)
	}
	c.fdQueue = nil

	if c.tr != nil {
		if c.xdgToplevelID != 0 {
			c.enc.Msg(c.xdgToplevelID, xdgToplevelReqDestroy, nil)
		}
		if c.xdgSurfaceID != 0 {
			c.enc.Msg(c.xdgSurfaceID, xdgSurfaceReqDestroy, nil)
		}
		if c.xdgWmBaseID != 0 {
			c.enc.Msg(c.xdgWmBaseID, xdgWmBaseReqDestroy, nil)
		}
		for _, b := range c.bufs {
			if b.id != 0 {
				c.enc.Msg(b.id, wlBufferReqDestroy, nil)
			}
		}
		if c.shmPoolID != 0 {
			c.enc.Msg(c.shmPoolID, wlShmPoolReqDestroy, nil)
		}
		if c.regionID != 0 {
			c.enc.Msg(c.regionID, wlRegionReqDestroy, nil)
		}
		if c.surfaceID != 0 {
			c.enc.Msg(c.surfaceID, wlSurfaceReqDestroy, nil)
		}
		if c.pointerID != 0 && c.seatVer >= 3 {
			c.enc.Msg(c.pointerID, wlPointerReqRelease, nil)
		}
		if c.keyboardID != 0 && c.seatVer >= 3 {
			c.enc.Msg(c.keyboardID, wlKeyboardReqRelease, nil)
		}
		if c.seatID != 0 && c.seatVer >= 5 {
			c.enc.Msg(c.seatID, wlSeatReqRelease, nil)
		}
		for oid, out := range c.outputs {
			if out.ver >= 3 {
				c.enc.Msg(oid, wlOutputReqRelease, nil)
			}
		}
		if c.wpPresID != 0 {
			c.enc.Msg(c.wpPresID, wpPresentationReqDestroy, nil)
		}
		_ = c.flush()
	}

	if c.shmCleanup != nil {
		c.shmCleanup()
		c.shmCleanup = nil
	}
	if c.tr != nil {
		return c.tr.Close()
	}
	return nil
}
