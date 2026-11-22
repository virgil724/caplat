// Command caplat measures a capture card's latency.
//
//	caplat list
//	caplat run --display auto --device "Live Gamer Ultra-Video" --duration 30s
//
// Connect an HDMI output of this PC to the capture card's input and extend
// the desktop onto it. caplat shows a frame-counter barcode on that output,
// takes each frame's scan-out (vblank) time from DXGI, reads the card back
// and reports arrival - vblank for every frame. Press Esc or click the
// barcode window to stop early.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/virgil724/caplat/pkg/caplat"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "list":
		err = list(os.Args[2:])
	case "run":
		err = run(os.Args[2:])
	case "audio":
		err = runAudio(os.Args[2:])
	case "sync":
		err = runSync(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "caplat:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `caplat measures a capture card's latency against the vblank of the HDMI output feeding it.

usage:
  caplat list [--ffmpeg PATH]
  caplat run [flags]          (caplat run -h for flags)
  caplat audio [flags]        audio latency (caplat audio -h for flags)
  caplat sync [flags]         video and audio together: A/V skew (caplat sync -h)

Connect an HDMI output of this PC to the card's input and extend the desktop
onto it. Press Esc or click the barcode window to stop early.
`)
}

func list(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	ff := fs.String("ffmpeg", "", "ffmpeg binary (default: ffmpeg on PATH)")
	fs.Parse(args)

	ds, err := caplat.Displays()
	if err != nil {
		return err
	}
	fmt.Println("Displays:")
	for _, d := range ds {
		var tags []string
		if d.Primary {
			tags = append(tags, "primary")
		}
		if d.LikelyCard {
			tags = append(tags, "looks like a capture card")
		}
		t := ""
		if len(tags) > 0 {
			t = "  [" + strings.Join(tags, ", ") + "]"
		}
		fmt.Printf("  %-14s %-22s %s%s\n", d.Name, d.Mode(), d.FriendlyName, t)
	}
	if outs, err := caplat.AudioOutputs(); err == nil {
		fmt.Println("\nAudio outputs:")
		for _, n := range outs {
			fmt.Printf("  %q\n", n)
		}
	}
	if ins, err := caplat.AudioInputs(); err == nil {
		fmt.Println("\nAudio inputs:")
		for _, n := range ins {
			fmt.Printf("  %q\n", n)
		}
	}
	ctx := context.Background()
	devs, err := caplat.Devices(ctx, *ff)
	if err != nil {
		return err
	}
	devLabel := "DirectShow"
	if runtime.GOOS != "windows" {
		devLabel = "V4L2"
	}
	fmt.Printf("\nCapture devices (%s):\n", devLabel)
	for _, dv := range devs {
		fmt.Printf("  %q\n", dv)
		modes, err := caplat.Modes(ctx, *ff, dv)
		if err != nil {
			fmt.Printf("      (%v)\n", firstLine(err.Error()))
			continue
		}
		printModes(modes)
	}
	return nil
}

// printModes prints one line per format: its sizes, each with its top frame
// rate.
func printModes(modes []caplat.Mode) {
	var order []string
	byFmt := map[string][]string{}
	seen := map[string]bool{}
	for _, m := range modes {
		size := fmt.Sprintf("%dx%d", m.MaxW, m.MaxH)
		if m.MinW != m.MaxW || m.MinH != m.MaxH {
			size = fmt.Sprintf("%dx%d-%dx%d", m.MinW, m.MinH, m.MaxW, m.MaxH)
		}
		entry := fmt.Sprintf("%s@%g", size, math.Round(m.MaxFPS*100)/100)
		if seen[m.Format+entry] {
			continue
		}
		seen[m.Format+entry] = true
		if byFmt[m.Format] == nil {
			order = append(order, m.Format)
		}
		byFmt[m.Format] = append(byFmt[m.Format], entry)
	}
	for _, f := range order {
		list := byFmt[f]
		if len(list) > 8 {
			list = append(list[:8:8], fmt.Sprintf("... (%d more)", len(byFmt[f])-8))
		}
		fmt.Printf("      %-8s %s\n", f, strings.Join(list, "  "))
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var cfg caplat.Config
	backendHelp := "capture backend: mf (Media Foundation; also reports the driver's timestamp), dshow (DirectShow in-process) or ffmpeg (DirectShow through ffmpeg)"
	displayHelp := `output feeding the card: \\.\DISPLAY3, DISPLAY3 or auto`
	ffInHelp := "extra ffmpeg options before -f dshow"
	if runtime.GOOS != "windows" {
		backendHelp = "capture backend: v4l2 (in-process; also reports the driver's timestamp) or ffmpeg (V4L2 through ffmpeg)"
		displayHelp = "output feeding the card: HDMI-A-1, DP-2 or auto"
		ffInHelp = "extra ffmpeg options before -f v4l2"
	}
	fs.StringVar(&cfg.Backend, "backend", caplat.DefaultBackend(), backendHelp)
	fs.StringVar(&cfg.Presenter, "presenter", caplat.PresenterAuto, "barcode presenter: auto or wayland")
	fs.StringVar(&cfg.Display, "display", "auto", displayHelp)
	fs.StringVar(&cfg.Device, "device", "", "capture device (default: the one that looks like a capture card)")
	size := fs.String("size", "1280x720", "capture size WxH")
	fs.StringVar(&cfg.PixelFormat, "pixel-format", "auto", "capture format: auto, nv12, yuyv422, mjpeg, ... (mf: auto, NV12, YUY2, ...)")
	fs.StringVar(&cfg.Preset, "preset", caplat.PresetLowLatency, "ffmpeg buffering: low-latency or default")
	fs.StringVar(&cfg.FFmpeg, "ffmpeg", "", "ffmpeg binary (default: ffmpeg on PATH)")
	ffIn := fs.String("ffmpeg-in", "", ffInHelp)
	ffOut := fs.String("ffmpeg-out", "", "extra ffmpeg options after -i")
	fs.DurationVar(&cfg.Duration, "duration", 30*time.Second, "how long to measure (0 = until Esc / Ctrl+C)")
	fs.DurationVar(&cfg.Interval, "interval", time.Minute, "print statistics every interval")
	csvPath := fs.String("csv", "", "write every matched frame to this CSV file")
	fs.BoolVar(&cfg.Force, "force", false, "allow measuring on the primary display")
	fs.Parse(args)

	w, h, err := parseSize(*size)
	if err != nil {
		return err
	}
	cfg.CaptureWidth, cfg.CaptureHeight = w, h
	cfg.FFmpegIn, cfg.FFmpegOut = strings.Fields(*ffIn), strings.Fields(*ffOut)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	setup, err := caplat.Prepare(ctx, cfg)
	if err != nil {
		return err
	}
	printSetup(setup)
	if *csvPath != "" {
		f, err := os.Create(*csvPath)
		if err != nil {
			return err
		}
		defer f.Close()
		cfg.CSV = f
	}

	updates := make(chan caplat.Update, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for u := range updates {
			if u.Interval != nil {
				line := fmt.Sprintf("[%s-%s] %s", round(u.Interval.Start), round(u.Interval.End), caplat.FormatStats(u.Interval.Stats))
				if d := u.Interval.Device; d.N > 0 {
					line += fmt.Sprintf("  | to driver timestamp p50 %.1f, after it %.1f", d.P50, u.Interval.Stats.P50-d.P50)
				}
				fmt.Println(line)
			}
		}
	}()
	fmt.Println("measuring... (Esc or click the barcode window to stop)")
	res, err := caplat.Run(ctx, cfg, updates)
	close(updates)
	<-done
	aborted := errors.Is(err, caplat.ErrAborted)
	if err != nil && !aborted {
		return err
	}
	if aborted {
		fmt.Println("stopped from the measurement window")
	}
	printResult(res)
	return nil
}

func parseSize(s string) (int, int, error) {
	a, b, ok := strings.Cut(strings.ToLower(s), "x")
	if !ok {
		return 0, 0, fmt.Errorf("bad --size %q, want WxH", s)
	}
	w, err1 := strconv.Atoi(a)
	h, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("bad --size %q, want WxH", s)
	}
	return w, h, nil
}

func round(d time.Duration) time.Duration { return d.Round(time.Second) }

func printSetup(s caplat.Setup) {
	fmt.Printf("output   %s %s  %s\n", s.Display, s.DisplayName, s.OutputMode)
	if s.Backend != caplat.BackendFFmpeg {
		fmt.Printf("capture  %q  %s\n", s.Device, s.CaptureMode)
		return
	}
	fmt.Printf("capture  %q  %s  preset %s\n", s.Device, s.CaptureMode, s.Preset)
	fmt.Printf("ffmpeg   %s\n", strings.Join(s.FFmpegArgs, " "))
}

func printResult(r caplat.Result) {
	c := r.Counts
	fmt.Printf("\nlatency vblank -> arrival over %s\n  %s\n", round(r.Elapsed), caplat.FormatStats(r.Total))
	fmt.Printf("  captured %d frames, barcode unreadable %d, presented but not captured %d, captured without vblank %d, mismatched %d\n",
		c.Captured, c.DecodeFail, c.OnlyVblank, c.OnlyCapture, c.Mismatched)
	fmt.Printf("  output %s, capture %s\n", r.Setup.OutputMode, r.Setup.CaptureMode)
	if d := r.Device; d.N > 0 {
		driverName := "Media Foundation"
		if r.Setup.Backend == caplat.BackendV4L2 {
			driverName = "V4L2"
		}
		fmt.Printf("\nsplit at the driver's capture timestamp (%s)\n", driverName)
		fmt.Printf("  vblank -> driver timestamp:  %s\n", caplat.FormatStats(d))
		fmt.Printf("  driver timestamp -> caplat:  p50 %.1f ms (rest of the frame over USB, driver, %s)\n", r.Total.P50-d.P50, driverName)
	}
}

func runAudio(args []string) error {
	fs := flag.NewFlagSet("audio", flag.ExitOnError)
	var cfg caplat.AudioConfig
	audioBackendHelp := "capture backend: wasapi (in-process; also reports the driver's timestamp) or ffmpeg (DirectShow through ffmpeg)"
	if runtime.GOOS != "windows" {
		audioBackendHelp = "capture backend: alsa (in-process via libasound; also reports the driver's timestamp) or ffmpeg (ALSA through ffmpeg)"
	}
	fs.StringVar(&cfg.Backend, "backend", caplat.DefaultAudioBackend(), audioBackendHelp)
	fs.StringVar(&cfg.Output, "output", "", "audio output feeding the card, or part of its name (default: the one that looks like the card)")
	fs.StringVar(&cfg.Input, "input", "", "the card's audio input, or part of its name (default: the one that looks like the card)")
	fs.StringVar(&cfg.Preset, "preset", caplat.PresetLowLatency, "ffmpeg buffering: low-latency or default")
	fs.StringVar(&cfg.FFmpeg, "ffmpeg", "", "ffmpeg binary (default: ffmpeg on PATH)")
	fs.BoolVar(&cfg.Exclusive, "exclusive", false, "wasapi: capture in exclusive mode (the device's minimum period, often 3 ms)")
	fs.DurationVar(&cfg.Period, "period", 0, "wasapi: ask for a shorter capture engine period, e.g. 3ms (IAudioClient3; 0 = default 10 ms)")
	fs.DurationVar(&cfg.Duration, "duration", 30*time.Second, "how long to measure (0 = until Ctrl+C)")
	fs.DurationVar(&cfg.Interval, "interval", 10*time.Second, "print statistics every interval")
	csvPath := fs.String("csv", "", "write every chirp to this CSV file")
	fs.Parse(args)
	if *csvPath != "" {
		f, err := os.Create(*csvPath)
		if err != nil {
			return err
		}
		defer f.Close()
		cfg.CSV = f
	}
	cfg.OnInterval = func(iv caplat.AudioInterval) {
		line := fmt.Sprintf("[%s-%s] %s", round(iv.Start), round(iv.End), caplat.FormatStats(iv.Delivered))
		if iv.Device.N > 0 {
			line += fmt.Sprintf("  | to driver timestamp p50 %.1f", iv.Device.P50)
		}
		fmt.Println(line)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fmt.Println("measuring audio: a 10 ms chirp every 500 ms... (Ctrl+C to stop)")
	res, err := caplat.RunAudio(ctx, cfg)
	fmt.Printf("output   %q (%s)\ninput    %q via %s\n", res.Output, res.OutputFormat, res.Input, res.Backend)
	if res.Backend == caplat.AudioWASAPI || res.Backend == caplat.AudioALSA {
		p := "default (about 10 ms)"
		if res.InputPeriod > 0 {
			p = res.InputPeriod.String()
		}
		fmt.Printf("period   %s\n", p)
	}
	if res.SoftwareMixer {
		fmt.Println("note     opened software mixer fallback (PipeWire/default) because hardware PCM was busy")
	}
	if len(res.FFmpegArgs) > 0 {
		fmt.Printf("ffmpeg   %s\n", strings.Join(res.FFmpegArgs, " "))
	}
	if res.Matched > 0 {
		fmt.Printf("\naudio latency, chirp played -> caplat has it, over %s\n  %s\n", round(res.Elapsed), caplat.FormatStats(res.Delivered))
		if d := res.Device; d.N > 0 {
			fmt.Printf("  chirp played -> driver's capture timestamp:  %s\n", caplat.FormatStats(d))
		}
		fmt.Printf("  chirps played %d, found %d, paired %d\n", res.Emitted, res.Detected, res.Matched)
	}
	return err
}

func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	var v caplat.Config
	var a caplat.AudioConfig
	syncVideoHelp := "video capture backend: mf, dshow or ffmpeg"
	syncAudioHelp := "audio capture backend: wasapi or ffmpeg"
	if runtime.GOOS != "windows" {
		syncVideoHelp = "video capture backend: v4l2 or ffmpeg"
		syncAudioHelp = "audio capture backend: alsa or ffmpeg"
	}
	fs.StringVar(&v.Backend, "backend", caplat.DefaultBackend(), syncVideoHelp)
	fs.StringVar(&v.Presenter, "presenter", caplat.PresenterAuto, "barcode presenter: auto or wayland")
	fs.StringVar(&a.Backend, "audio-backend", caplat.DefaultAudioBackend(), syncAudioHelp)
	fs.StringVar(&v.Display, "display", "auto", "output feeding the card")
	fs.StringVar(&v.Device, "device", "", "video capture device")
	size := fs.String("size", "1280x720", "capture size WxH")
	fs.StringVar(&a.Output, "output", "", "audio output feeding the card")
	fs.StringVar(&a.Input, "input", "", "the card's audio input")
	fs.StringVar(&v.Preset, "preset", caplat.PresetLowLatency, "ffmpeg buffering (both): low-latency or default")
	fs.DurationVar(&v.Duration, "duration", 30*time.Second, "how long to measure (0 = until Esc / Ctrl+C)")
	fs.BoolVar(&v.Force, "force", false, "allow measuring on the primary display")
	fs.Parse(args)
	w, h, err := parseSize(*size)
	if err != nil {
		return err
	}
	v.CaptureWidth, v.CaptureHeight = w, h
	a.Preset = v.Preset

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	setup, err := caplat.Prepare(ctx, v)
	if err != nil {
		return err
	}
	printSetup(setup)
	fmt.Println("measuring video and audio together... (Esc or click the barcode window to stop)")
	res, err := caplat.RunAV(ctx, v, a, nil)
	aborted := errors.Is(err, caplat.ErrAborted)
	if err != nil && !aborted {
		return err
	}
	fmt.Printf("audio    %q -> %q via %s\n", res.Audio.Output, res.Audio.Input, res.Audio.Backend)
	fmt.Printf("\nvideo  vblank -> caplat:        %s\n", caplat.FormatStats(res.Video.Total))
	fmt.Printf("audio  played -> caplat:        %s\n", caplat.FormatStats(res.Audio.Delivered))
	if sk := res.Skew(); !math.IsNaN(sk) {
		fmt.Printf("\nA/V skew at caplat: audio %+.1f ms vs video (%s)\n", sk, lateWord(sk))
	}
	if d := res.DriverSkew(); !math.IsNaN(d) {
		fmt.Printf("  at the drivers' timestamps: video %.1f ms, audio %.1f ms -> audio %+.1f ms (%s)\n",
			res.Video.Device.P50, res.Audio.Device.P50, d, lateWord(d))
	}
	return nil
}

func lateWord(skew float64) string {
	if skew >= 0 {
		return "sound later than picture"
	}
	return "sound earlier than picture"
}
