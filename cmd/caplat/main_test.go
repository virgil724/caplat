package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/virgil724/caplat/pkg/caplat"
)

func TestFprintModes(t *testing.T) {
	var buf bytes.Buffer
	fprintModes(&buf, []caplat.Mode{
		{Format: "nv12", MinW: 1920, MinH: 1080, MaxW: 1920, MaxH: 1080, MaxFPS: 60},
		{Format: "nv12", MinW: 1920, MinH: 1080, MaxW: 1920, MaxH: 1080, MaxFPS: 60},
		{Format: "nv12", MinW: 1280, MinH: 720, MaxW: 1280, MaxH: 720, MaxFPS: 59.94006},
		{Format: "mjpeg", MinW: 3840, MinH: 2160, MaxW: 3840, MaxH: 2160, MaxFPS: 0},
		{Format: "mjpeg", MinW: 640, MinH: 360, MaxW: 1920, MaxH: 1080, MaxFPS: 0},
	})
	got := buf.String()
	want := "      nv12     1920x1080@60  1280x720@59.94\n" +
		"      mjpeg    3840x2160@?  640x360-1920x1080@?\n"
	if got != want {
		t.Fatalf("fprintModes:\n%q\nwant:\n%q", got, want)
	}
	if strings.Contains(got, "@0") {
		t.Errorf("unexpected @0 in output: %q", got)
	}
}

func TestRepeatFormatting(t *testing.T) {
	t.Run("run_summary", func(t *testing.T) {
		var buf bytes.Buffer
		fprintRunSummary(&buf, caplat.RunResult{
			Run:    1,
			Total:  caplat.Stats{N: 600, P50: 16.2, P99: 16.4},
			Device: caplat.Stats{N: 600, P50: 0.2},
		}, 3)
		fprintRunSummary(&buf, caplat.RunResult{
			Run:   2,
			Total: caplat.Stats{N: 590, P50: 18.8, P99: 19.1},
		}, 3)
		fprintRunSummary(&buf, caplat.RunResult{
			Run:   3,
			Total: caplat.Stats{N: 0},
		}, 3)
		want := "run 1/3: n=600  p50 16.2 ms  p99 16.4 ms  phase 0.2 ms\n" +
			"run 2/3: n=590  p50 18.8 ms  p99 19.1 ms\n" +
			"run 3/3: n=0\n"
		if buf.String() != want {
			t.Fatalf("got:\n%s\nwant:\n%s", buf.String(), want)
		}
	})

	t.Run("repeat_summary_odd_with_phase", func(t *testing.T) {
		var buf bytes.Buffer
		fprintRepeatSummary(&buf, []caplat.RunResult{
			{Run: 1, Total: caplat.Stats{N: 50, P50: 35.5}, Device: caplat.Stats{N: 50, P50: 18.8}},
			{Run: 2, Total: caplat.Stats{N: 50, P50: 16.2}, Device: caplat.Stats{N: 50, P50: 0.2}},
			{Run: 3, Total: caplat.Stats{N: 50, P50: 18.8}, Device: caplat.Stats{N: 50, P50: 5.7}},
		})
		want := "\nrepeat summary (3 runs)\n" +
			"  run p50s:                        35.5, 16.2, 18.8 ms\n" +
			"  p50 min/median/max:              16.2 / 18.8 / 35.5 ms\n" +
			"  phase (vblank -> driver ts p50): 18.8, 0.2, 5.7 ms\n" +
			"  phase min/median/max:            0.2 / 5.7 / 18.8 ms\n"
		if buf.String() != want {
			t.Fatalf("got:\n%s\nwant:\n%s", buf.String(), want)
		}
	})

	t.Run("repeat_summary_even_no_phase", func(t *testing.T) {
		var buf bytes.Buffer
		fprintRepeatSummary(&buf, []caplat.RunResult{
			{Run: 1, Total: caplat.Stats{N: 50, P50: 20.0}},
			{Run: 2, Total: caplat.Stats{N: 50, P50: 16.0}},
			{Run: 3, Total: caplat.Stats{N: 50, P50: 18.0}},
			{Run: 4, Total: caplat.Stats{N: 50, P50: 36.0}},
		})
		got := buf.String()
		if !strings.Contains(got, "p50 min/median/max:              16.0 / 19.0 / 36.0 ms") {
			t.Errorf("even median mismatch:\n%s", got)
		}
		if strings.Contains(got, "phase") {
			t.Errorf("unexpected phase lines without driver timestamps:\n%s", got)
		}
	})
}
