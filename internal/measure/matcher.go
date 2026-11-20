package measure

import (
	"sync"

	"github.com/virgil724/caplat/internal/barcode"
)

// Sample is one matched frame.
type Sample struct {
	Frame     uint64 // presenter frame number (unwrapped)
	VblankQPC int64  // when the frame started scanning out
	ArriveQPC int64  // when the captured frame carrying it was read
	LatencyMS float64
}

// Counts are frames that could not be matched.
type Counts struct {
	OnlyVblank  uint64 // presented, never seen in the capture
	OnlyCapture uint64 // captured, no vblank time for it
	DecodeFail  uint64 // captured frames whose barcode did not decode (after the first one did)
	Captured    uint64 // captured frames read
	Mismatched  uint64 // paired, but arrived before its vblank (not counted as samples)
}

type pending struct {
	vblank, arrive int64 // 0 = not yet
	seenAt         int64 // qpc of the first event, for expiry
	matched        bool
}

// Matcher pairs presenter vblank times with capture arrival times. It is
// safe for concurrent use: the presenter and the capture reader feed it from
// different goroutines.
type Matcher struct {
	freq   float64 // QPC ticks per millisecond
	expire int64   // ticks after which an unmatched entry is counted and dropped
	onPair func(Sample)

	mu     sync.Mutex
	pend   map[uint64]*pending
	latest uint64 // highest presented frame, for unwrapping
	counts Counts
	lastGC int64
	// seenCode is set once any barcode decoded: frames before that (black
	// picture while the capture and the barcode window start) are not
	// decode failures.
	seenCode bool
	// stopped is set once the barcode window closed: the frames that still
	// arrive show the desktop, not failures.
	stopped bool
}

// PresenterStopped marks the end of the barcode; later unreadable frames are
// not counted. Frames still in flight are matched as usual.
func (m *Matcher) PresenterStopped() {
	m.mu.Lock()
	m.stopped = true
	m.mu.Unlock()
}

// NewMatcher returns a matcher. qpcFreq is ticks per second; onPair is called
// (under the matcher lock, so keep it short) for every matched frame.
func NewMatcher(qpcFreq int64, onPair func(Sample)) *Matcher {
	return &Matcher{
		freq:   float64(qpcFreq) / 1000,
		expire: qpcFreq * 5,
		onPair: onPair,
		pend:   make(map[uint64]*pending),
	}
}

// Vblank records that frame f started scanning out at qpc.
func (m *Matcher) Vblank(f uint64, qpc int64) {
	if f == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if f > m.latest {
		m.latest = f
	}
	p := m.entry(f, qpc)
	if p.vblank == 0 {
		p.vblank = qpc
		m.tryPair(f, p)
	}
	m.gc(qpc)
}

// Captured records a captured frame read at qpc carrying the 24-bit code,
// or a decode failure when ok is false.
func (m *Matcher) Captured(code uint32, ok bool, qpc int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts.Captured++
	if !ok {
		if m.seenCode && !m.stopped {
			m.counts.DecodeFail++
		}
		return
	}
	m.seenCode = true
	if code == 0 {
		return // warm-up frames
	}
	f := m.unwrap(code)
	p := m.entry(f, qpc)
	if p.arrive == 0 { // the first capture of a frame is the one that counts
		p.arrive = qpc
		m.tryPair(f, p)
	}
	m.gc(qpc)
}

// unwrap maps a 24-bit code to the full frame number closest to the latest
// presented frame.
func (m *Matcher) unwrap(code uint32) uint64 {
	const span = uint64(barcode.Mask) + 1
	base := m.latest &^ uint64(barcode.Mask)
	f := base | uint64(code)
	if f > m.latest+span/2 && f >= span {
		f -= span
	} else if f+span/2 < m.latest {
		f += span
	}
	return f
}

func (m *Matcher) entry(f uint64, qpc int64) *pending {
	p := m.pend[f]
	if p == nil {
		p = &pending{seenAt: qpc}
		m.pend[f] = p
	}
	return p
}

func (m *Matcher) tryPair(f uint64, p *pending) {
	if p.matched || p.vblank == 0 || p.arrive == 0 {
		return
	}
	p.matched = true
	if p.arrive < p.vblank {
		// Arrived before it was shown: two frames were confused (e.g. the
		// presenter raced ahead while its window was hidden). Not a sample.
		m.counts.Mismatched++
		return
	}
	if m.onPair != nil {
		m.onPair(Sample{Frame: f, VblankQPC: p.vblank, ArriveQPC: p.arrive,
			LatencyMS: float64(p.arrive-p.vblank) / m.freq})
	}
}

// gc drops entries older than the expiry window about once a second,
// counting the ones that never matched.
func (m *Matcher) gc(now int64) {
	if now-m.lastGC < m.expire/5 {
		return
	}
	m.lastGC = now
	m.sweep(now - m.expire)
}

func (m *Matcher) sweep(before int64) {
	for f, p := range m.pend {
		if p.seenAt > before {
			continue
		}
		if !p.matched {
			if p.vblank != 0 {
				m.counts.OnlyVblank++
			} else {
				m.counts.OnlyCapture++
			}
		}
		delete(m.pend, f)
	}
}

// Flush counts everything still unmatched (end of run).
func (m *Matcher) Flush() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweep(1<<63 - 1)
}

// Counts returns the unmatched counters so far.
func (m *Matcher) Counts() Counts {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts
}
