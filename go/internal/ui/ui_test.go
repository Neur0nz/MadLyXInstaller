package ui

import (
	"bytes"
	"strings"
	"testing"
)

// Plain mode exists because pterm keeps emitting in-place redraws when output
// is piped: measured at 29 carriage returns and out-of-order lines for a
// five-item progress bar, which would wreck a log file.
func TestPlainModeEmitsNoRedraws(t *testing.T) {
	var buf bytes.Buffer
	u := NewFor(&buf, Plain, false)

	u.Section("Installing MiKTeX")
	u.Begin("downloading")
	for i := 1; i <= 5; i++ {
		u.Progress("package", i, 5)
	}
	u.End("downloading", true, "installed")
	u.Warn("not elevated")
	u.End("", false, "culmus.sty not found")

	out := buf.String()
	if strings.Contains(out, "\r") {
		t.Errorf("plain mode emitted a carriage return (in-place redraw):\n%q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("plain mode emitted an ANSI escape:\n%q", out)
	}
}

// Every progress item must leave a durable line, so a piped log shows all 24
// package installs rather than one redrawn bar.
func TestPlainModeKeepsEveryProgressLine(t *testing.T) {
	var buf bytes.Buffer
	u := NewFor(&buf, Plain, false)
	u.Begin("packages")
	for i := 1; i <= 24; i++ {
		u.Progress("pkg", i, 24)
	}
	if n := strings.Count(buf.String(), "[24/24]"); n != 1 {
		t.Errorf("expected the final item once, got %d", n)
	}
	if n := strings.Count(buf.String(), "/24]"); n != 24 {
		t.Errorf("expected 24 durable progress lines, got %d", n)
	}
}

// The bug that crashed the PowerShell version, and that pterm's own confirm
// still has: asking a question when nothing can answer it.
func TestConfirmWithoutATerminalUsesTheDefault(t *testing.T) {
	for _, def := range []bool{true, false} {
		var buf bytes.Buffer
		u := NewFor(&buf, Plain, false)
		got := u.Confirm("Disable Alt+Shift?", "some detail", def)
		if got != def {
			t.Errorf("Confirm returned %v, want the default %v", got, def)
		}
		if !strings.Contains(buf.String(), "not interactive") {
			t.Errorf("Confirm did not say why it was not asking:\n%s", buf.String())
		}
	}
}

func TestCanPromptReflectsConstruction(t *testing.T) {
	if NewFor(&bytes.Buffer{}, Rich, true).CanPrompt() != true {
		t.Error("CanPrompt should be true when constructed as promptable")
	}
	if NewFor(&bytes.Buffer{}, Plain, false).CanPrompt() != false {
		t.Error("CanPrompt should be false when constructed as non-promptable")
	}
}

func TestSummaryRendersInPlainMode(t *testing.T) {
	var buf bytes.Buffer
	u := NewFor(&buf, Plain, false)
	u.Summary([][]string{
		{"Step", "Result"},
		{"LyX", "done"},
		{"MadLyX settings", "already done"},
	})
	out := buf.String()
	for _, want := range []string{"LyX", "done", "MadLyX settings", "already done"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lost %q:\n%s", want, out)
		}
	}
}

// A step must be able to end without a spinner having been started, and vice
// versa, without panicking.
func TestOutOfOrderCallsAreSafe(t *testing.T) {
	var buf bytes.Buffer
	u := NewFor(&buf, Plain, false)
	u.End("nope", true, "finished with no step started")
	u.Detail("detail with no step")
	u.Progress("thing", 1, 1)
	u.Begin("started")
	u.Begin("started again without finishing the first")
	u.Skipped("skipped")
	u.End("", false, "failed with no step")
}
