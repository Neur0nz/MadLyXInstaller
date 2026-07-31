// Package ui is the only place in the installer that writes to the terminal.
//
// Everything else returns values and errors; nothing else prints. That split
// is deliberate - in the PowerShell version output was fused into the logic
// (8-30% of every module was Write-* calls), so no function could be reused or
// tested without also producing console noise.
//
// Two pterm behaviours are load-bearing here, both established by measurement:
//
//   - Its interactive prompts block forever when stdin is redirected. We never
//     call them; Confirm checks the terminal first.
//   - Its spinners and progress bars keep emitting in-place redraws when output
//     is piped, producing 29 carriage returns and out-of-order lines in a log
//     file. All live rendering is gated on stdout being a terminal.
//
// Several steps run at once, so everything here is safe to call concurrently
// and a single spinner reports all of them together.
package ui

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pterm/pterm"
	"golang.org/x/term"
)

// Mode decides how much the terminal can be asked to do.
type Mode int

const (
	// Rich renders spinners and colour. Chosen when stdout is a terminal.
	Rich Mode = iota
	// Plain emits one durable line per event: no colour, no redraws, no ANSI.
	Plain
)

// UI carries terminal capabilities and the assumptions that follow from them.
type UI struct {
	mode      Mode
	canPrompt bool
	out       io.Writer

	mu      sync.Mutex
	running map[string]time.Time // step name -> when it started
	detail  string               // most recent activity from any running step
	spin    *pterm.SpinnerPrinter
}

// New inspects the real terminal state once. The answer cannot change during a
// run, so it is decided here rather than rechecked at each call site.
func New(assumeYes bool) *UI {
	stdoutTTY := term.IsTerminal(int(os.Stdout.Fd()))
	stdinTTY := term.IsTerminal(int(os.Stdin.Fd()))

	mode := Plain
	if stdoutTTY {
		mode = Rich
	}
	return NewFor(os.Stdout, mode, stdoutTTY && stdinTTY && !assumeYes)
}

// NewFor builds a UI with the terminal state supplied rather than detected, so
// tests can exercise both modes without a real terminal.
func NewFor(out io.Writer, mode Mode, canPrompt bool) *UI {
	if mode == Plain {
		pterm.DisableColor()
		pterm.DisableStyling()
	}
	return &UI{mode: mode, canPrompt: canPrompt, out: out, running: map[string]time.Time{}}
}

// CanPrompt reports whether asking the user a question can actually work.
// Callers must consult this before anything that needs an answer - notably
// elevation, where a UAC dialog with nobody present is worse than skipping.
func (u *UI) CanPrompt() bool { return u.canPrompt }

// Mode exposes the detected mode, mainly so tests can assert on it.
func (u *UI) Mode() Mode { return u.mode }

// Title prints the banner.
func (u *UI) Title(name, version string) {
	if u.mode == Plain {
		fmt.Fprintf(u.out, "%s %s\n", name, version)
		return
	}
	pterm.DefaultHeader.WithFullWidth().Printfln("%s  %s", name, version)
	fmt.Fprintln(u.out)
}

// Section starts a named phase of work.
func (u *UI) Section(title string) {
	u.mu.Lock()
	u.stopSpinnerLocked()
	u.mu.Unlock()
	if u.mode == Plain {
		fmt.Fprintf(u.out, "\n== %s\n", title)
		return
	}
	pterm.DefaultSection.Println(title)
}

// Begin announces that a step has started. Several may be active at once.
func (u *UI) Begin(step string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.running[step] = time.Now()
	u.detail = ""

	if u.mode == Plain {
		fmt.Fprintf(u.out, "-> %s\n", step)
		return
	}
	u.refreshLocked()
}

// End reports that a step finished. ok=false renders it as a failure.
func (u *UI) End(step string, ok bool, msg string) {
	u.mu.Lock()
	started, wasRunning := u.running[step]
	delete(u.running, step)
	elapsed := ""
	if wasRunning {
		elapsed = " " + humanDuration(time.Since(started))
	}
	// Stop the shared spinner before printing, so the completion line is not
	// overwritten by the next redraw.
	u.stopSpinnerLocked()
	u.mu.Unlock()

	line := msg
	if line == "" {
		line = step
	}
	switch {
	case u.mode == Plain && ok:
		fmt.Fprintf(u.out, "   ok: %s%s\n", line, elapsed)
	case u.mode == Plain:
		fmt.Fprintf(u.out, "   FAIL: %s\n", line)
	case ok:
		pterm.Success.Println(line + pterm.Gray(elapsed))
	default:
		pterm.Error.Println(line)
	}

	// Anything still running keeps its spinner.
	u.mu.Lock()
	u.refreshLocked()
	u.mu.Unlock()
}

// Skipped reports a step that needed no work.
func (u *UI) Skipped(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	u.mu.Lock()
	u.stopSpinnerLocked()
	u.mu.Unlock()
	if u.mode == Plain {
		fmt.Fprintf(u.out, "   skip: %s\n", msg)
	} else {
		pterm.Info.Println(msg)
	}
	u.mu.Lock()
	u.refreshLocked()
	u.mu.Unlock()
}

// Progress reports position within a step, e.g. package 7 of 24.
func (u *UI) Progress(text string, current, total int) {
	if u.mode == Plain {
		// One durable line per item rather than a redrawn bar.
		fmt.Fprintf(u.out, "   [%d/%d] %s\n", current, total, text)
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.detail = fmt.Sprintf("%s [%d/%d]", text, current, total)
	u.refreshLocked()
}

// Detail updates the current activity without changing position.
func (u *UI) Detail(text string) {
	if u.mode == Plain {
		fmt.Fprintf(u.out, "   %s\n", text)
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.detail = text
	u.refreshLocked()
}

// Warn reports something the user should know that is not fatal.
func (u *UI) Warn(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	u.mu.Lock()
	u.stopSpinnerLocked()
	u.mu.Unlock()
	if u.mode == Plain {
		fmt.Fprintf(u.out, "   warn: %s\n", msg)
	} else {
		pterm.Warning.Println(msg)
	}
	u.mu.Lock()
	u.refreshLocked()
	u.mu.Unlock()
}

// Fail reports a failure.
func (u *UI) Fail(format string, a ...any) { u.End("", false, fmt.Sprintf(format, a...)) }

// Done reports a success outside any step.
func (u *UI) Done(format string, a ...any) { u.End("", true, fmt.Sprintf(format, a...)) }

// Info prints an indented note.
func (u *UI) Info(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if u.mode == Plain {
		fmt.Fprintf(u.out, "   %s\n", msg)
		return
	}
	fmt.Fprintf(u.out, "   %s\n", pterm.Gray(msg))
}

// refreshLocked redraws the shared spinner for whatever is currently running.
// Callers must hold u.mu.
func (u *UI) refreshLocked() {
	if u.mode != Rich {
		return
	}
	if len(u.running) == 0 {
		u.stopSpinnerLocked()
		return
	}

	names := make([]string, 0, len(u.running))
	oldest := time.Now()
	for n, t := range u.running {
		names = append(names, n)
		if t.Before(oldest) {
			oldest = t
		}
	}
	sort.Strings(names)

	text := strings.Join(names, ", ")
	if u.detail != "" {
		text += " - " + u.detail
	}
	text += "  " + humanDuration(time.Since(oldest))

	if u.spin == nil {
		u.spin, _ = pterm.DefaultSpinner.WithRemoveWhenDone(true).Start(text)
		return
	}
	u.spin.UpdateText(text)
}

func (u *UI) stopSpinnerLocked() {
	if u.spin != nil {
		_ = u.spin.Stop()
		u.spin = nil
	}
}

func humanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("(%ds)", int(d.Seconds()))
	}
	return fmt.Sprintf("(%dm%02ds)", int(d.Minutes()), int(d.Seconds())%60)
}

// Confirm asks a yes/no question.
//
// When prompting is impossible it returns def without asking and says so, so a
// piped or unattended run behaves predictably. Callers pass def=false for
// anything that changes the system beyond the installer's own files, so an
// unattended run never silently edits Defender configuration.
//
// This deliberately does not use pterm.DefaultInteractiveConfirm, which blocks
// forever on redirected stdin.
func (u *UI) Confirm(question, detail string, def bool) bool {
	if !u.canPrompt {
		shown := "no"
		if def {
			shown = "yes"
		}
		fmt.Fprintf(u.out, "\n ? %s\n   (not interactive - using default: %s)\n", question, shown)
		return def
	}

	// Steps that prompt never run alongside others, so the spinner can be
	// stopped outright rather than restored afterwards.
	u.mu.Lock()
	u.stopSpinnerLocked()
	u.mu.Unlock()

	fmt.Fprintln(u.out)
	if u.mode == Rich {
		pterm.DefaultBasicText.Println(pterm.Bold.Sprint(" ? " + question))
	} else {
		fmt.Fprintf(u.out, " ? %s\n", question)
	}
	for _, line := range strings.Split(strings.TrimSpace(detail), "\n") {
		if u.mode == Rich {
			fmt.Fprintf(u.out, "   %s\n", pterm.Gray(line))
		} else {
			fmt.Fprintf(u.out, "   %s\n", line)
		}
	}

	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	for {
		fmt.Fprintf(u.out, "   %s ", hint)
		var answer string
		if _, err := fmt.Fscanln(os.Stdin, &answer); err != nil {
			// Input ended (closed pipe, Ctrl+Z). Do not loop on it.
			fmt.Fprintf(u.out, "\n   (no more input - using the default)\n")
			return def
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "":
			return def
		case "y", "yes":
			return true
		case "n", "no":
			return false
		default:
			fmt.Fprintln(u.out, "   Please answer y or n.")
		}
	}
}

// Summary prints the closing report as a table in Rich mode.
func (u *UI) Summary(rows [][]string) {
	u.mu.Lock()
	u.stopSpinnerLocked()
	u.mu.Unlock()
	fmt.Fprintln(u.out)
	if u.mode == Plain {
		for _, r := range rows {
			fmt.Fprintf(u.out, "%s: %s\n", r[0], strings.Join(r[1:], " "))
		}
		return
	}
	_ = pterm.DefaultTable.WithHasHeader().WithData(rows).Render()
}
