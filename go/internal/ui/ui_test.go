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

// The live block gives every running step its own line. One shared line was
// what made a real install lie: "installing LyX" and "mathtools [7/35]"
// overwrote each other while both steps were genuinely running.
func TestLiveBlockGivesEachStepItsOwnLine(t *testing.T) {
	u := NewFor(&bytes.Buffer{}, Rich, false)
	u.Overall(2, 13)
	u.Begin("LyX")
	u.Begin("Hebrew LaTeX packages")
	u.DetailFor("LyX", "downloading 41.2 MB of 57.6 MB")
	u.ProgressFor("Hebrew LaTeX packages", "mathtools", 7, 35)

	u.mu.Lock()
	frame := u.frameLocked()
	u.mu.Unlock()

	lines := strings.Split(frame, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected a header and one line per step, got %d:\n%s", len(lines), frame)
	}
	if !strings.Contains(lines[0], "2/13") {
		t.Errorf("header lost the overall count: %q", lines[0])
	}
	if !strings.Contains(lines[1], "LyX") || !strings.Contains(lines[1], "41.2 MB") {
		t.Errorf("LyX line wrong: %q", lines[1])
	}
	if !strings.Contains(lines[2], "mathtools [7/35]") {
		t.Errorf("packages line wrong: %q", lines[2])
	}
}

// A finished step must leave the block, or its stale status stays on screen.
func TestFinishedStepsLeaveTheLiveBlock(t *testing.T) {
	u := NewFor(&bytes.Buffer{}, Rich, false)
	u.Begin("alpha")
	u.Begin("bravo")
	u.End("alpha", true, "alpha")

	u.mu.Lock()
	frame, running := u.frameLocked(), len(u.order)
	u.mu.Unlock()

	if running != 1 {
		t.Fatalf("expected 1 step still running, got %d", running)
	}
	if strings.Contains(frame, "alpha") {
		t.Errorf("finished step still shown: %q", frame)
	}
}

// A detail longer than the terminal would wrap, and a wrapped line desyncs the
// redraw: the area printer would clear fewer lines than it drew.
func TestLongDetailIsTruncatedToTheTerminalWidth(t *testing.T) {
	u := NewFor(&bytes.Buffer{}, Rich, false)
	u.width = 60
	u.Begin("step")
	u.DetailFor("step", strings.Repeat("verylongdetail ", 40))

	u.mu.Lock()
	frame := u.frameLocked()
	u.mu.Unlock()

	for _, line := range strings.Split(frame, "\n") {
		if n := len([]rune(stripANSI(line))); n > u.width {
			t.Errorf("line is %d columns, wider than the %d-column terminal: %q", n, u.width, line)
		}
	}
}

// Detail with several steps running has no honest owner. Guessing is what the
// shared line did, so it must land nowhere rather than on the wrong step.
func TestUnattributedDetailIsDroppedWhenAmbiguous(t *testing.T) {
	u := NewFor(&bytes.Buffer{}, Rich, false)
	u.Begin("alpha")
	u.Begin("beta")
	u.Detail("who said this?")

	u.mu.Lock()
	frame := u.frameLocked()
	u.mu.Unlock()

	if strings.Contains(frame, "who said this?") {
		t.Errorf("ambiguous detail was attributed to a step anyway: %q", frame)
	}

	// With exactly one step running there is an honest answer, so use it.
	u.End("beta", true, "beta")
	u.Detail("now unambiguous")
	u.mu.Lock()
	frame = u.frameLocked()
	u.mu.Unlock()
	if !strings.Contains(frame, "now unambiguous") {
		t.Errorf("detail was dropped even though only one step was running: %q", frame)
	}
}

func TestFitPadsAndTruncates(t *testing.T) {
	if got := fit("abc", 6); got != "abc   " {
		t.Errorf("fit padded to %q", got)
	}
	if got := fit("abcdefgh", 4); got != "abc…" {
		t.Errorf("fit truncated to %q", got)
	}
	if got := fit("abc", 0); got != "" {
		t.Errorf("fit with no room returned %q", got)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
