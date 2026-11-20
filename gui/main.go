// Command caplat-gui is the desktop front end of caplat: pick the output that
// feeds the capture card and the card itself, then watch the latency live.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"

	"github.com/virgil724/caplat/pkg/caplat"
)

var (
	sizes     = []string{"1280x720", "1920x1080", "2560x1440", "3840x2160"}
	formats   = []string{"auto", "nv12", "yuyv422", "yuv420p", "mjpeg"}
	presets   = []string{caplat.PresetLowLatency, caplat.PresetDefault}
	backends  = []string{backendMF, backendFFmpeg}
	durations = []string{"10 s", "30 s", "1 min", "5 min", "30 min", "Until stopped"}
)

const (
	backendMF     = "Media Foundation"
	backendFFmpeg = "ffmpeg (DirectShow)"
)

var durationValue = map[string]time.Duration{
	"10 s": 10 * time.Second, "30 s": 30 * time.Second, "1 min": time.Minute,
	"5 min": 5 * time.Minute, "30 min": 30 * time.Minute, "Until stopped": 0,
}

// point is one second of the run: when, and its p50 and p90.
type point struct{ t, p50, p90 float64 }

type app struct {
	win *mygo.Window

	// Choices.
	displays     []caplat.Display
	displayLabel []string
	display      string
	devices      []string
	device       string
	size         string
	format       string
	preset       string
	backend      string
	duration     string
	loading      bool
	askPrimary   bool

	// The run.
	running bool
	cancel  context.CancelFunc
	setup   caplat.Setup
	series  []point
	total   caplat.Stats
	counts  caplat.Counts
	hist    *caplat.Histogram
	driver  caplat.Stats // vblank -> driver timestamp (Media Foundation)
	elapsed time.Duration
	status  string
	err     string
	csvPath string // temporary CSV of the last run, for Export
}

func newApp() *app {
	return &app{size: sizes[0], format: formats[0], preset: presets[0], backend: backends[0], duration: durations[1], loading: true}
}

func displayLabel(d caplat.Display) string {
	l := strings.TrimPrefix(d.Name, `\\.\`) + " · " + d.Mode()
	if d.FriendlyName != "" {
		l += " · " + d.FriendlyName
	}
	if d.Primary {
		l += " (primary)"
	}
	return l
}

// load lists displays and capture devices (ffmpeg takes a moment).
func (a *app) load() {
	ds, derr := caplat.Displays()
	devs, verr := caplat.DevicesMF()
	if verr != nil || len(devs) == 0 {
		devs, verr = caplat.Devices(context.Background(), "")
	}
	a.win.Update(func() { a.loaded(ds, devs, errors.Join(derr, verr)) })
}

func (a *app) loaded(ds []caplat.Display, devs []string, err error) {
	a.loading = false
	a.displays, a.displayLabel = ds, nil
	for _, d := range ds {
		a.displayLabel = append(a.displayLabel, displayLabel(d))
		if d.LikelyCard && !d.Primary && a.display == "" {
			a.display = displayLabel(d)
		}
	}
	if a.display == "" && len(a.displayLabel) > 0 {
		a.display = a.displayLabel[len(a.displayLabel)-1]
	}
	a.devices = devs
	if a.device == "" {
		a.device = caplat.PickDevice(devs)
	}
	if err != nil {
		a.err = err.Error()
	}
}

func (a *app) chosenDisplay() (caplat.Display, bool) {
	for i, l := range a.displayLabel {
		if l == a.display {
			return a.displays[i], true
		}
	}
	return caplat.Display{}, false
}

func (a *app) config() (caplat.Config, error) {
	d, ok := a.chosenDisplay()
	if !ok {
		return caplat.Config{}, errors.New("choose the display that feeds the capture card")
	}
	if a.device == "" {
		return caplat.Config{}, errors.New("choose the capture device")
	}
	var w, h int
	fmt.Sscanf(a.size, "%dx%d", &w, &h)
	return caplat.Config{
		Display: d.Name, Device: a.device, CaptureWidth: w, CaptureHeight: h,
		PixelFormat: a.format, Preset: a.preset, Duration: durationValue[a.duration],
		Backend:  map[string]string{backendMF: caplat.BackendMF, backendFFmpeg: caplat.BackendFFmpeg}[a.backend],
		Interval: time.Minute, Force: d.Primary,
	}, nil
}

// start begins a run; confirmed is true once the user accepted measuring on
// the primary display.
func (a *app) start(confirmed bool) {
	cfg, err := a.config()
	if err != nil {
		a.err = err.Error()
		return
	}
	if d, _ := a.chosenDisplay(); d.Primary && !confirmed {
		a.askPrimary = true
		return
	}
	f, err := os.CreateTemp("", "caplat-*.csv")
	if err != nil {
		a.err = err.Error()
		return
	}
	cfg.CSV = f
	ctx, cancel := context.WithCancel(context.Background())
	a.running, a.cancel, a.err, a.status = true, cancel, "", "Starting…"
	a.series, a.total, a.counts, a.hist, a.elapsed, a.csvPath = nil, caplat.Stats{}, caplat.Counts{}, nil, 0, f.Name()
	a.driver = caplat.Stats{}

	go func() {
		if setup, err := caplat.Prepare(ctx, cfg); err == nil {
			a.win.Update(func() { a.setup, a.status = setup, "Measuring — press Esc or click the barcode to stop" })
		}
		updates := make(chan caplat.Update, 8)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for u := range updates {
				a.win.Update(func() { a.apply(u) })
			}
		}()
		res, err := caplat.Run(ctx, cfg, updates)
		close(updates)
		<-done
		f.Close()
		a.win.Update(func() { a.finish(res, err) })
	}()
}

func (a *app) apply(u caplat.Update) {
	a.elapsed, a.total, a.counts, a.hist, a.driver = u.Elapsed, u.Total, u.Counts, u.Hist, u.Device
	if u.Second.N > 0 {
		a.series = append(a.series, point{t: u.Elapsed.Seconds(), p50: u.Second.P50, p90: u.Second.P90})
	}
}

func (a *app) finish(r caplat.Result, err error) {
	a.running, a.cancel = false, nil
	if r.Total.N > 0 {
		a.total, a.counts, a.elapsed, a.setup, a.driver = r.Total, r.Counts, r.Elapsed, r.Setup, r.Device
		h := r.Hist
		a.hist = &h
	}
	switch {
	case errors.Is(err, caplat.ErrAborted):
		a.status = "Stopped from the measurement window"
	case err != nil && !errors.Is(err, context.Canceled):
		a.err, a.status = err.Error(), ""
	default:
		a.status = fmt.Sprintf("Done: %d frames over %s", r.Total.N, r.Elapsed.Round(time.Second))
	}
}

func (a *app) stop() {
	if a.cancel != nil {
		a.cancel()
		a.status = "Stopping…"
	}
}

func (a *app) export() {
	if a.csvPath == "" {
		return
	}
	dst, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, DefaultPath: "caplat.csv"})
	if err != nil || dst == "" {
		return
	}
	if err := copyFile(a.csvPath, dst); err != nil {
		a.err = err.Error()
	} else {
		a.status = "Saved " + dst
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ---- view ----

func (a *app) view(c *ui.Context) {
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.settings(c)
		ui.Divider(c)
		a.results(c)
	})
	if ui.AlertDialog(c, &a.askPrimary, "Measure on the primary display?",
		"The barcode covers the whole display until the run ends. Press Esc or click it to stop.",
		"Cancel", "Measure") == 1 {
		a.start(true)
	}
}

func (a *app) settings(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(330).Padding(20).Gap(14).Children(func() {
		ui.Text(c, "caplat").FontSize(22).Bold()
		ui.Text(c, "Connect an HDMI output of this PC to the capture card's input and extend the desktop onto it.").
			TextColor(t.TextMuted).FontSize(12)
		if a.loading {
			ui.Text(c, "Looking for displays and capture devices…").TextColor(t.TextMuted)
			return
		}
		busy := a.running
		// field is a caption over a select; in a row, fields share the width.
		field := func(label string, value *string, options []string) ui.Element {
			return ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, label).FontSize(11).TextColor(t.TextMuted)
				ui.Select(c, value, options).Label(label).Disabled(busy)
			})
		}
		field("Output feeding the card", &a.display, a.displayLabel)
		field("Capture device", &a.device, a.devices)
		ui.Row(c).Gap(10).Children(func() {
			field("Capture size", &a.size, sizes).Grow(1).Basis(0)
			field("Format", &a.format, formats).Grow(1).Basis(0)
		})
		ui.Row(c).Gap(10).Children(func() {
			field("Capture API", &a.backend, backends).Grow(1).Basis(0)
			field("Duration", &a.duration, durations).Grow(1).Basis(0)
		})
		if a.backend == backendFFmpeg {
			field("ffmpeg buffering", &a.preset, presets)
		}
		ui.Spacer(c)
		if a.running {
			if ui.Button(c, "Stop").Clicked() {
				a.stop()
			}
		} else if ui.PrimaryButton(c, "Start").Clicked() {
			a.start(false)
		}
	})
}

func (a *app) results(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).Padding(20).Gap(14).Children(func() {
		if a.setup.Display != "" {
			capture := a.setup.CaptureMode
			if a.setup.Preset != "" {
				capture += ", ffmpeg " + a.setup.Preset
			}
			ui.Textf(c, "Output %s (%s) %s  →  capture %s",
				strings.TrimPrefix(a.setup.Display, `\\.\`), a.setup.DisplayName, a.setup.OutputMode,
				capture).FontSize(12).TextColor(t.TextMuted)
		}
		a.statCards(c)
		ui.Text(c, "Latency per second (p50, p90)").Bold()
		a.lineChart(c)
		ui.Text(c, "Distribution").Bold()
		a.histogram(c)
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if a.err != "" {
				ui.Text(c, a.err).TextColor(t.Danger).Grow(1)
			} else {
				ui.Text(c, a.status).TextColor(t.TextMuted).Grow(1)
			}
			if ui.Button(c, "Export CSV").Disabled(a.running || a.csvPath == "").Clicked() {
				a.export()
			}
		})
	})
}

func ms(v float64) string {
	if math.IsNaN(v) || v == 0 {
		return "–"
	}
	return fmt.Sprintf("%.1f", v)
}

func (a *app) statCards(c *ui.Context) {
	t := c.Theme()
	s := a.total
	cards := []struct{ label, value string }{
		{"p50 ms", ms(s.P50)}, {"p10–p90", ms(s.P10) + "–" + ms(s.P90)}, {"p99", ms(s.P99)},
		{"min / max", ms(s.Min) + " / " + ms(s.Max)}, {"frames", fmt.Sprint(s.N)},
		{"unreadable", fmt.Sprint(a.counts.DecodeFail)}, {"not captured", fmt.Sprint(a.counts.OnlyVblank)},
		{"time", a.elapsed.Round(time.Second).String()},
	}
	if a.driver.N > 0 {
		cards = append(cards,
			struct{ label, value string }{"to driver timestamp", ms(a.driver.P50)},
			struct{ label, value string }{"after it", ms(s.P50 - a.driver.P50)})
	}
	ui.Row(c).Gap(10).Wrap().Children(func() {
		for _, k := range cards {
			ui.Column(c).Padding(10, 14).Radius(8).Border(1, t.Border).MinWidth(96).Children(func() {
				ui.Text(c, k.label).FontSize(11).TextColor(t.TextMuted)
				ui.Text(c, k.value).FontSize(20).Bold()
			})
		}
	})
}

func (a *app) lineChart(c *ui.Context) {
	t := c.Theme()
	pts := a.series
	if len(pts) == 0 {
		ui.Box(c).Height(180).Radius(8).Border(1, t.Border).Center().Children(func() {
			ui.Text(c, "No data yet").TextColor(t.TextMuted)
		})
		return
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, p := range pts {
		lo, hi = math.Min(lo, p.p50), math.Max(hi, p.p90)
	}
	x := chart.NewLinear(0, math.Max(pts[len(pts)-1].t, 10)).Nice(5)
	y := chart.NewLinear(math.Max(0, lo-2), hi+2).Nice(4)
	yt := y.Ticks(4)
	xs := make([]float32, len(pts))
	p50 := make([]float32, len(pts))
	p90 := make([]float32, len(pts))
	for i, p := range pts {
		xs[i], p50[i], p90[i] = x.At(p.t), y.At(p.p50), y.At(p.p90)
	}
	chart.Plot(c, chart.Axes{Left: yt, Bottom: x.Ticks(5)}, func(area ui.Element) {
		area.Draw(func(p *ui.Painter, r ui.Rect) {
			for _, tk := range yt {
				p.Line(r.X, chart.Y(r, tk.At), r.X+r.W, chart.Y(r, tk.At), 1, t.Border)
			}
			p.StrokePath(chart.Line(r, xs, p90, chart.CurveLinear), 1.5, t.Accent.Alpha(0.4))
			p.StrokePath(chart.Line(r, xs, p50, chart.CurveLinear), 2, t.Accent)
		})
	}).Label("Latency over time").Height(180).Gap(6)
}

func (a *app) histogram(c *ui.Context) {
	t := c.Theme()
	h := a.hist
	if h == nil || h.N == 0 {
		ui.Box(c).Height(150).Radius(8).Border(1, t.Border)
		return
	}
	// Show from p0.5 - 2 ms to p99.5 + 2 ms in about 80 bars.
	lo := math.Max(0, math.Floor(h.Quantile(0.005))-2)
	hi := math.Ceil(h.Quantile(0.995)) + 2
	const bars = 80
	width := (hi - lo) / bars
	counts := make([]float64, bars)
	maxC := 0.0
	for i, n := range h.Bins {
		v := (float64(i) + 0.5) * 0.1
		if n == 0 || v < lo || v >= hi {
			continue
		}
		b := min(int((v-lo)/width), bars-1)
		counts[b] += float64(n)
		maxC = math.Max(maxC, counts[b])
	}
	x := chart.NewLinear(lo, hi)
	y := chart.NewLinear(0, maxC)
	chart.Plot(c, chart.Axes{Bottom: x.Ticks(6)}, func(area ui.Element) {
		area.Draw(func(p *ui.Painter, r ui.Rect) {
			for i, n := range counts {
				if n == 0 {
					continue
				}
				x0 := chart.X(r, x.At(lo+float64(i)*width))
				x1 := chart.X(r, x.At(lo+float64(i+1)*width))
				top := chart.Y(r, y.At(n))
				p.Fill(ui.Rect{X: x0, Y: top, W: max(x1-x0-1, 1), H: r.Y + r.H - top}, t.Accent, 1)
			}
		})
	}).Label("Latency distribution (ms)").Height(150).Gap(6)
}

func main() {
	a := newApp()
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "caplat",
			Width:     1100,
			Height:    760,
			MinWidth:  900,
			MinHeight: 640,
			StateKey:  "main",
			Content:   ui.View(a.view),
		})
		go a.load()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
