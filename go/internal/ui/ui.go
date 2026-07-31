// Package ui is the only place in the installer that writes to the terminal.
//
// Everything else returns values and errors; nothing else prints. That split
// is deliberate - in the PowerShell version output was fused into the logic
// (8-30% of every module was Write-* calls), so no function could be reused
// or tested without also producing console noise.
//
// Two behaviours are load-bearing and were established by measurement rather
// than assumption:
//
//   - pterm's interactive prompts block forever when stdin is redirected. We
//     never call them; Confirm below checks the terminal first.
//   - pterm's spinners and progress bars keep emitting in-place redraws when
//     output is piped, producing 29 carriage returns and out-of-order lines in
//     a log file. We gate all live rendering on stdout being a terminal.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pterm/pterm"
	"golang.org/x/term"
)

// Mode decides how much the terminal can be asked to do.
type Mode int

const (
	// Rich renders spinners, progress bars and colour. Chosen when stdout is a
	// terminal.
	Rich Mode = iota
	// Plain emits one durable line per event: no colour, no redraws, no ANSI.
	// Chosen when output is piped or redirected.
	Plain
)

// UI carries terminal capabilities and the assumptions that follow from them.
type UI struct {
	mode        Mode
	canPrompt   bool
	out         io.Writer
	mu          sync.Mutex
	activeSpin  *pterm.SpinnerPrinter
	spinStarted time.Time
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

	u := &UI{
		mode: mode,
		// Prompting needs a real terminal on *both* ends. assumeYes (from
		// --yes / --unattended) disables questions entirely.
		canPrompt: stdoutTTY && stdinTTY && !assumeYes,
		out:       os.Stdout,
	}
	if mode == Plain {
		pterm.DisableColor()
		pterm.DisableStyling()
	}
	return u
}

// NewFor builds a UI with the terminal state supplied rather than detected, so
// tests can exercise both modes without a real terminal.
func NewFor(out io.Writer, mode Mode, canPrompt bool) *UI {
	if mode == Plain {
		pterm.DisableColor()
		pterm.DisableStyling()
	}
	return &UI{mode: mode, canPrompt: canPrompt, out: out}
}

// CanPrompt reports whether asking the user a question can actually work.
// Callers must consult this before doing anything that needs an answer -
// notably elevation, where a UAC dialog with nobody present is worse than
// skipping the step.
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
	u.stopSpinner("")
	if u.mode == Plain {
		fmt.Fprintf(u.out, "\n== %s\n", title)
		return
	}
	pterm.DefaultSection.Println(title)
}

// Step announces a long-running operation. In Rich mode it becomes a live
// spinner with elapsed time, which is the entire point: the PowerShell version
// had ten operations with timeouts of two minutes or more that printed one line
// and then nothing, and users reasonably concluded it had hung.
//
// Call Done, Failed or Skipped to finish it.
func (u *UI) Step(text string) {
	u.stopSpinner("")
	u.mu.Lock()
	u.spinStarted = time.Now()
	u.mu.Unlock()

	if u.mode == Plain {
		fmt.Fprintf(u.out, "-> %s\n", text)
		return
	}
	sp, _ := pterm.DefaultSpinner.WithRemoveWhenDone(false).Start(text)
	u.mu.Lock()
	u.activeSpin = sp
	u.mu.Unlock()
}

// Progress reports position within the current step, e.g. package 7 of 24.
func (u *UI) Progress(text string, current, total int) {
	elapsed := u.elapsed()
	if u.mode == Plain {
		// One durable line per item rather than a redrawn bar.
		fmt.Fprintf(u.out, "   [%d/%d] %s\n", current, total, text)
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.activeSpin != nil {
		u.activeSpin.UpdateText(fmt.Sprintf("%s  [%d/%d]  %s", text, current, total, elapsed))
	}
}

// Detail updates the current step's text without changing its position.
func (u *UI) Detail(text string) {
	elapsed := u.elapsed()
	if u.mode == Plain {
		fmt.Fprintf(u.out, "   %s\n", text)
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.activeSpin != nil {
		u.activeSpin.UpdateText(fmt.Sprintf("%s  %s", text, elapsed))
	}
}

func (u *UI) elapsed() string {
	u.mu.Lock()
	started := u.spinStarted
	u.mu.Unlock()
	if started.IsZero() {
		return ""
	}
	d := time.Since(started).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("(%ds)", int(d.Seconds()))
	}
	return fmt.Sprintf("(%dm%02ds)", int(d.Minutes()), int(d.Seconds())%60)
}

func (u *UI) stopSpinner(final string) {
	u.mu.Lock()
	sp := u.activeSpin
	u.activeSpin = nil
	u.mu.Unlock()
	if sp != nil {
		if final != "" {
			sp.Success(final)
		} else {
			_ = sp.Stop()
		}
	}
}

// Done ends the active step successfully.
func (u *UI) Done(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if u.mode == Plain {
		u.stopSpinner("")
		fmt.Fprintf(u.out, "   ok: %s\n", msg)
		return
	}
	u.mu.Lock()
	sp := u.activeSpin
	u.activeSpin = nil
	u.mu.Unlock()
	if sp != nil {
		sp.Success(msg)
		return
	}
	pterm.Success.Println(msg)
}

// Skipped ends the active step as already-satisfied.
func (u *UI) Skipped(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if u.mode == Plain {
		u.stopSpinner("")
		fmt.Fprintf(u.out, "   skip: %s\n", msg)
		return
	}
	u.mu.Lock()
	sp := u.activeSpin
	u.activeSpin = nil
	u.mu.Unlock()
	if sp != nil {
		sp.Info(msg)
		return
	}
	pterm.Info.Println(msg)
}

// Warn reports something the user should know that is not fatal.
func (u *UI) Warn(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	u.stopSpinner("")
	if u.mode == Plain {
		fmt.Fprintf(u.out, "   warn: %s\n", msg)
		return
	}
	pterm.Warning.Println(msg)
}

// Fail reports a failure.
func (u *UI) Fail(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	u.mu.Lock()
	sp := u.activeSpin
	u.activeSpin = nil
	u.mu.Unlock()
	if sp != nil {
		sp.Fail(msg)
		return
	}
	if u.mode == Plain {
		fmt.Fprintf(u.out, "   FAIL: %s\n", msg)
		return
	}
	pterm.Error.Println(msg)
}

// Info prints an indented note.
func (u *UI) Info(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if u.mode == Plain {
		fmt.Fprintf(u.out, "   %s\n", msg)
		return
	}
	fmt.Fprintf(u.out, "   %s\n", pterm.Gray(msg))
}

// Confirm asks a yes/no question.
//
// When prompting is impossible it returns def without asking and says so, so a
// piped or unattended run behaves predictably. Callers pass def=false for
// anything that changes the system beyond the installer's own files, so that
// an unattended run never silently edits the registry or Defender config.
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

	u.stopSpinner("")
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
	u.stopSpinner("")
	fmt.Fprintln(u.out)
	if u.mode == Plain {
		for _, r := range rows {
			fmt.Fprintf(u.out, "%s: %s\n", r[0], strings.Join(r[1:], " "))
		}
		return
	}
	_ = pterm.DefaultTable.WithHasHeader().WithData(rows).Render()
}
