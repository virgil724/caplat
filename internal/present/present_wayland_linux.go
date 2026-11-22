//go:build linux

package present

import (
	"errors"

	"github.com/virgil724/caplat/displays"
	"github.com/virgil724/caplat/internal/barcode"
	"github.com/virgil724/caplat/internal/wayland"
)

type waylandPresenter struct {
	client *wayland.Client
}

// NewWayland returns the Linux Wayland wl_shm + wp_presentation presenter.
func NewWayland() Presenter { return &waylandPresenter{} }

// New returns the default Linux barcode presenter (Wayland).
func New() Presenter { return NewWayland() }

func (p *waylandPresenter) Open(d displays.Display, onVblank func(frame uint64, qpc int64)) error {
	c, err := wayland.Connect(nil)
	if err != nil {
		return err
	}
	if err := c.Open(d, onVblank); err != nil {
		_ = c.Close()
		if errors.Is(err, wayland.ErrAborted) {
			return ErrAborted
		}
		return err
	}
	p.client = c
	return nil
}

func (p *waylandPresenter) Present(frame uint64) error {
	err := p.client.Present(frame, func(buf []byte, w, stride int) {
		cell := w / barcode.Cells
		DrawBand(buf, w, stride, cell, barcode.Encode(uint32(frame&barcode.Mask)))
	})
	if errors.Is(err, wayland.ErrAborted) {
		return ErrAborted
	}
	return err
}

func (p *waylandPresenter) Close() error {
	if p.client == nil {
		return nil
	}
	err := p.client.Close()
	p.client = nil
	return err
}
