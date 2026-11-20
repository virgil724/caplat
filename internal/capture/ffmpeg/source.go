package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// Frame is one captured picture: an 8-bit luma plane of Width x Height.
type Frame struct {
	Y   []byte
	QPC int64 // when the whole frame had been read
}

// Source runs ffmpeg and delivers frames to a callback.
type Source struct {
	cfg  Config
	now  func() int64
	cmd  *exec.Cmd
	errs tail
	done chan error
}

// Start launches ffmpeg. onFrame runs on the reader goroutine for every frame
// with the arrival time from now(); it must return quickly and must not keep
// f.Y (the buffer is reused).
func Start(ctx context.Context, cfg Config, now func() int64, onFrame func(f Frame)) (*Source, error) {
	args, err := Args(cfg)
	if err != nil {
		return nil, err
	}
	bin := cfg.FFmpeg
	if bin == "" {
		bin = "ffmpeg"
	}
	s := &Source{cfg: cfg, now: now, done: make(chan error, 1)}
	s.cmd = exec.CommandContext(ctx, bin, args...)
	s.cmd.Stderr = &s.errs
	out, err := s.cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := s.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	go s.read(out, onFrame)
	return s, nil
}

func (s *Source) read(r io.Reader, onFrame func(Frame)) {
	buf := make([]byte, FrameBytes(s.cfg.PixelFormat, s.cfg.Width, s.cfg.Height))
	luma := buf[:s.cfg.Width*s.cfg.Height]
	var rerr error
	for {
		if _, rerr = io.ReadFull(r, buf); rerr != nil {
			break
		}
		onFrame(Frame{Y: luma, QPC: s.now()})
	}
	werr := s.cmd.Wait()
	if errors.Is(rerr, io.EOF) && werr == nil {
		s.done <- nil
		return
	}
	s.done <- fmt.Errorf("ffmpeg stopped (read: %v, exit: %v): %s", rerr, werr, s.errs.String())
}

// Done delivers the reason ffmpeg stopped (once).
func (s *Source) Done() <-chan error { return s.done }

// Stop kills ffmpeg.
func (s *Source) Stop() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

// tail keeps the last 2 KiB written to it.
type tail struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.Write(p)
	if t.buf.Len() > 2048 {
		b := t.buf.Bytes()
		t.buf.Reset()
		t.buf.Write(b[len(b)-2048:])
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(t.buf.String())
}
