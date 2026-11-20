# caplat

**Measure a capture card's latency with nothing but an HDMI cable.**

caplat feeds a capture card from your own PC and times every frame:
from the moment the frame starts leaving the GPU (its vblank) to the moment
your capture software has it in memory. No photodiode, no high-speed camera,
no special hardware. It runs in about 30 seconds and reports every frame, so
you see both the latency and the jitter.

```
output   \\.\DISPLAY3 AVT GC553  3840x2160@60.000
capture  "Live Gamer Ultra-Video"  1280x720@60.000 NV12 (Media Foundation)

latency vblank -> arrival over 22s
  n=1197  min 17.9  p10 17.9  p50 18.1  p90 18.1  p99 18.2  max 18.5  mean 18.0 ms

split at the driver's capture timestamp (Media Foundation)
  vblank -> driver timestamp:  n=1197  min 6.2  p10 6.2  p50 6.2  p90 6.2  p99 6.2  max 7.0  mean 6.2 ms
  driver timestamp -> caplat:  p50 11.9 ms (rest of the frame over USB, driver, Media Foundation)
```

It measures **audio** the same way (a chirp on the HDMI audio, found again
by cross-correlation) and **A/V sync** (both at once through the same
link). It comes as a command-line tool and a desktop app (built with
[MyGo](https://github.com/egoist/mygo)), and its capture code is usable as
a library (see [Use it in your program](#use-it-in-your-program)).

![caplat GUI](docs/gui.png)

## How it works

1. Connect an HDMI output of the PC to the capture card's **input** and
   extend the desktop onto it. Windows sees the card as another monitor.
2. caplat opens a full-screen window on that output and shows, on every
   refresh, a barcode along the top edge: a 24-bit frame number with a
   CRC-4 and sync cells, sized relative to the width so it survives any
   scaling the card does.
3. For every frame it asks DXGI when that frame started scanning out
   (`IDXGISwapChain::GetFrameStatistics`, a QueryPerformanceCounter time of
   the vblank). The PC's own compositor and presentation queue are not
   counted.
4. It reads the card back, decodes the barcode in every captured frame and
   notes when the frame arrived, on the same QueryPerformanceCounter clock.
   Two capture backends:
   - `mf`: Media Foundation in-process (`IMFSourceReader`, low-latency
     mode). It also gets the driver's capture timestamp of every frame
     (`MFSampleExtension_DeviceTimestamp`, on the same clock), which splits
     the latency in two. No ffmpeg needed.
   - `dshow`: DirectShow in-process (capture filter -> SampleGrabber ->
     NullRenderer, no graph clock, samples handed over in `SampleCB`).
   - `ffmpeg`: an ffmpeg subprocess reading DirectShow, as most streaming
     software does.
5. latency = arrival − vblank, for every frame. Statistics live in a fixed
   0.1 ms histogram, so a run can last hours.

What the number includes: the HDMI transmission of the frame (a frame has to
be received whole before a card can deliver it, 16.7 ms at 60 Hz), the card's
processing and scaling, USB transfer, the driver, and the capture API
(Media Foundation, or DirectShow and ffmpeg).
That is the latency a streaming or remote-play app built on the card
actually pays.

## What it found

AVerMedia **GC553** (Live Gamer Ultra), 4K60 in, 1280x720@60 NV12 capture:

| Capture path | p50 | p10–p90 |
|---|---|---|
| **Media Foundation**, in-process | **18.1 ms** | 0.2 ms |
| **DirectShow**, in-process | **17.9 ms** | 0.1 ms |
| ffmpeg + DirectShow, `low-latency` preset (`-fflags nobuffer -flags low_delay -probesize 32 -analyzeduration 0`, `-fps_mode passthrough`) | 35.2 ms | 0.4 ms |
| ffmpeg + DirectShow, `default` (plain `ffmpeg -f dshow`) | 59.6 ms (or 76 ms, decided at start-up) | 15 ms |

The card itself is fast: receiving a 60 Hz frame over HDMI takes about
16 ms, and Media Foundation has it 18 ms after vblank. The driver stamps a
frame 6.2 ms after vblank, as it starts to arrive. **ffmpeg costs one whole
extra frame** even with every low-latency option: DirectShow read
in-process is as fast as Media Foundation, so the frame is queued in
ffmpeg, not in DirectShow.

Through OBS (OBS reads the GC553 with DirectShow, then its virtual camera
read by caplat in-process) it is 53 ms: about 18 ms for the card, the rest
OBS's compositing at its own frame rate (drifting against the output's
vblank) and the virtual camera.

With default options ffmpeg probes the input while the device opens and that
read-ahead never drains, because frames are then read exactly as fast as
they arrive: one or two frames of extra latency, chosen at random each time
the capture starts, plus about a frame of jitter.

### Long runs: one frame that never comes back

Over minutes the low-latency capture of the GC553 sometimes steps from
35 ms to 52 ms, exactly one frame more, and stays there until the capture is
restarted. It happens on its own (after about 15 s in one run, 95 s in
another, not at all in a 90 s run) and also right after a deliberate CPU
stall; once it has happened, further stalls cause long spikes but no second
step.
Shrinking ffmpeg's real-time buffer (`-rtbufsize 2M`) does not prevent it.
Media Foundation never does it: under the same CPU stalls it spikes and
comes straight back to 18.1 ms, and the driver timestamps stay at 6.2 ms
throughout. So the frame is queued in ffmpeg, not in the driver. Any
software reading the card through ffmpeg can carry it; a long `--interval`
run shows it as a step.

### Audio and A/V sync

`caplat audio` plays a 10 ms 1-8 kHz chirp every 500 ms (plus a random
0-20 ms, so chirps fall at every phase of the capture's packets) on the HDMI
audio of the output feeding the card, and finds each one in the card's audio
by normalized cross-correlation: exact to a sample, indifferent to volume and
filtering. The time a chirp was played comes from `IAudioClock::GetPosition`
(the device position and its QPC time), so the PC's audio engine buffering is
not counted.

| GC553 audio capture | played -> driver timestamp | played -> program |
|---|---|---|
| **WASAPI exclusive**, 3 ms periods (`--exclusive`) | about 7-20 ms (see below) | driver timestamp + 0-3 ms (p50 1.5) |
| WASAPI shared, 10 ms periods | about 7-20 ms | driver timestamp + 0-19 ms (p50 9.4) |
| ffmpeg + DirectShow, `-audio_buffer_size 10` | | 21.6 ms |
| ffmpeg + DirectShow, defaults | | 210 ms |

The GC553's audio delay is a sawtooth: it creeps up by about 0.26 ms per
second, then drops by about 20 ms, over and over: the card's audio clock and
the PC's HDMI audio clock disagree and the card slips its buffer. Within a
short run it looks constant (7 ms one run, 21 ms another).

Every endpoint we tried offers only 10 ms shared-mode periods
(`IAudioClient3` cannot go lower on them); exclusive mode is what makes the
packets small.

`caplat sync` measures video and audio at once. Both are timed from when the
PC put them out, on one clock, so audio p50 minus video p50 is the skew the
capture adds: on the GC553 the sound arrives 1-6 ms after the picture it
belongs to, far below what anyone notices (ITU-R BT.1359: 45 ms late).

caplat keeps the display awake during a run and stops with an error if the
barcode stops being shown (display asleep, window covered), instead of
measuring garbage.

## Install

Nothing else is needed for the default Media Foundation backend.
[ffmpeg](https://ffmpeg.org/download.html) on your `PATH` is only needed for
`--backend ffmpeg`.

- **Desktop app**: download `caplat-gui.exe` from the
  [releases](https://github.com/virgil724/caplat/releases).
- **CLI**: download `caplat.exe`, or `go install github.com/virgil724/caplat/cmd/caplat@latest`.

## Use

```
caplat list
```
lists the displays (marking the primary one and those whose EDID name looks
like a capture card) and the DirectShow devices with their modes (through
ffmpeg, if it is installed).

```
caplat run --device "Live Gamer Ultra-Video" --duration 30s
caplat audio --exclusive --duration 30s
caplat sync --duration 30s
```

| Flag | Default | |
|---|---|---|
| `--backend` | `mf` | `mf` (Media Foundation, in-process, with driver timestamps), `dshow` (DirectShow, in-process) or `ffmpeg` (DirectShow through ffmpeg) |
| `--display` | `auto` | `\\.\DISPLAY3`, `DISPLAY3`, or `auto` (the one display whose EDID name looks like a capture card) |
| `--device` | auto | capture device; auto picks the capture card |
| `--size` | `1280x720` | capture size |
| `--pixel-format` | `auto` | mf: `NV12`, `YUY2`, ...; ffmpeg: `nv12`, `yuyv422`, `mjpeg`, ...; auto prefers uncompressed formats |
| `--preset` | `low-latency` | ffmpeg only: `low-latency` or `default` |
| `--ffmpeg-in`, `--ffmpeg-out` | | ffmpeg only: extra options before `-f dshow` / after `-i` |
| `--duration` | `30s` | `0` runs until you stop it |
| `--interval` | `1m` | print statistics every interval (to see drift on long runs) |
| `--csv` | | write every matched frame (frame, vblank, arrival, latency, driver timestamp) |
| `--force` | | allow measuring on the primary display |

`caplat audio` takes `--backend wasapi|ffmpeg`, `--output` and `--input`
(audio endpoints, or part of their names; auto picks the card's),
`--exclusive`, `--period`, `--preset`, `--duration`, `--interval` and `--csv`.
`caplat sync` takes the video flags plus `--audio-backend`, `--output` and
`--input`. The desktop app has the same three modes (Video, Audio, A/V sync).

Press **Esc** or click the barcode window to stop early; the results so far
are printed. caplat refuses the primary display unless you pass `--force`,
because the barcode covers the whole screen.

The frame rate follows the output's refresh rate, fractional ones included
(`60000/1001`). Both the output mode and the capture mode are printed with
every result: Windows may drive the card at 4K or 30 Hz without telling you.

## Use it in your program

The in-process capture is importable (Windows, pure Go, no cgo):

- `github.com/virgil724/caplat/capture/mf`: Media Foundation video capture.
  Every frame comes with its planes as delivered (`Data`, `Pitch`,
  `FourCC`), the arrival time and the driver's capture timestamp, both on
  the QueryPerformanceCounter clock (`capture.QPC`).
- `github.com/virgil724/caplat/capture/wasapi`: WASAPI audio capture,
  shared or exclusive mode, as interleaved float32 packets with the driver's
  capture timestamp.

```go
src, err := mf.Start(mf.Config{Device: "Live Gamer Ultra-Video", Width: 1280, Height: 720, FPS: 60, Format: "NV12"},
	capture.QPC, capture.QPCFrequency(), func(f capture.Frame) {
		// f.Data, f.Pitch: NV12, valid during the callback
	})
```

## How this compares

| Tool | Measures | Needs |
|---|---|---|
| **caplat** | capture card + capture software | an HDMI cable |
| [NVIDIA Holoscan latency tool](https://docs.nvidia.com/holoscan/archive/2.4.0/latency_tool.html) | video I/O loopback | NVIDIA dev kits, AJA cards, Linux |
| [disguise capture latency](https://help.disguise.one/hardware/performance/capture-latency-methodology) | capture cards (barcode in frame) | disguise media servers |
| [Time Sleuth](https://consolemods.org/wiki/AV:Time_Sleuth), Leo Bodnar lag tester | displays | a photodiode device |
| stopwatch + phone camera | anything | patience; ±1 frame |

## Build

```
go build ./cmd/caplat                      # CLI, any recent Go
cd gui && go build -ldflags -H=windowsgui  # GUI, Go 1.27.4+ (MyGo)
```

`go test ./...` covers the barcode, the matcher, the histogram, the chirp
detector, struct layouts of the Windows bindings and ffmpeg argument
building; the GUI has view tests (`cd gui && go test`).

caplat is pure Go: Direct3D 11, DXGI, Media Foundation, DirectShow, WASAPI
and the Win32 calls go through `syscall` by COM vtable index (see
`internal/win`).

## Roadmap

- Find where in ffmpeg the extra frame is queued (and whether it can be drained).
- Rarely, a Media Foundation frame carries the previous frame's driver timestamp (one sample in thousands, 16.7 ms off).
- Linux: DRM vblank events and V4L2.

## License

MIT
