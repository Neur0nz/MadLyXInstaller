// Package ui is the only place in the installer that writes to the terminal.
//
// Everything else returns values and errors; nothing else prints. That split
// is deliberate - in the PowerShell version output was fused into the logic
// (8-30% of every module was Write-* calls), so no function could be reused or
// tested without also producing console noise.
//
// There are two backends and the choice is made once, from whether stdout is a
// terminal:
//
//   - Rich drives a Bubble Tea program (see tui.go): a live status block, a
//     scrollable history, and prompts answered with a keypress.
//   - Plain emits one durable line per event: no colour, no redraws, no ANSI.
//     This is what CI, a pipe and the log file get, and it is not a degraded
//     mode - a piped run must produce a readable transcript.
//
// Several steps run at once, so every method here is safe to call concurrently.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Mode decides how much the terminal can be asked to do.
type Mode int

const (
	// Rich renders the interactive display. Chosen when stdout is a terminal.
	Rich Mode = iota
	// Plain emits one durable line per event.
	Plain
)

// UI carries terminal capabilities and the assumptions that follow from them.
type UI struct {
	mode      Mode
	canPrompt bool
	out       io.Writer

	mu   sync.Mutex
	prog *tea.Program // nil in Plain mode
	done chan struct{}
}

// New inspects the real terminal state once. The answer cannot change during a
// run, so it is decided here rather than rechecked at each call site.
func New(assumeYes bool) *UI {
	stdoutTTY := term.IsTerminal(int(os.Stdout.Fd()))
	stdinTTY := term.IsTerminal(int(os.Stdin.Fd()))

	if !stdoutTTY {
		return NewFor(os.Stdout, Plain, false)
	}
	u := NewFor(os.Stdout, Rich, stdinTTY && !assumeYes)
	u.start()
	return u
}

// NewFor builds a UI with the terminal state supplied rather than detected, so
// tests can exercise Plain mode without a real terminal. It does not start a
// Bubble Tea program; the model is tested directly instead (see tui_test.go).
func NewFor(out io.Writer, mode Mode, canPrompt bool) *UI {
	return &UI{mode: mode, canPrompt: canPrompt, out: out}
}

// start launches the display on its own goroutine.
//
// Bubble Tea owns the terminal for as long as it runs, so the installer cannot
// also be on that goroutine; it stays on the caller's and communicates only by
// sending messages.
func (u *UI) start() {
	m := newModel()
	u.prog = tea.NewProgram(m, tea.WithMouseCellMotion())
	u.done = make(chan struct{})
	go func() {
		defer close(u.done)
		_, _ = u.prog.Run()
	}()
}

// send posts a message to the display, or does nothing in Plain mode.
func (u *UI) send(msg tea.Msg) {
	u.mu.Lock()
	p := u.prog
	u.mu.Unlock()
	if p != nil {
		p.Send(msg)
	}
}

// Close tears the display down. Safe to call more than once.
func (u *UI) Close() {
	u.mu.Lock()
	p := u.prog
	u.prog = nil
	u.mu.Unlock()
	if p == nil {
		return
	}
	p.Quit()
	<-u.done
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
	u.send(titleMsg{version: version})
}

// Section starts a named phase of work.
func (u *UI) Section(title string) {
	if u.mode == Plain {
		fmt.Fprintf(u.out, "\n== %s\n", title)
		return
	}
	u.send(noteMsg{kind: "section", text: title})
}

// Overall records how much of the plan has settled.
func (u *UI) Overall(done, total int) {
	if u.mode == Rich {
		u.send(overallMsg{done: done, total: total})
	}
}

// Begin announces that a step has started. Several may be active at once.
func (u *UI) Begin(step string) {
	if u.mode == Plain {
		fmt.Fprintf(u.out, "-> %s\n", step)
		return
	}
	u.send(beginMsg{step: step})
}

// End reports that a step finished. ok=false renders it as a failure.
func (u *UI) End(step string, ok bool, msg string) {
	if u.mode == Plain {
		line := msg
		if line == "" {
			line = step
		}
		if ok {
			fmt.Fprintf(u.out, "   ok: %s\n", line)
		} else {
			fmt.Fprintf(u.out, "   FAIL: %s\n", line)
		}
		return
	}
	u.send(endMsg{step: step, ok: ok, msg: msg})
}

// Drop removes a step from the display without announcing an outcome.
//
// Used when an optional step fails: the caller follows with a warning, and
// ending it as a success first printed "SUCCESS Defender exclusions" directly
// above "WARNING Defender exclusions did not complete", which read as both.
func (u *UI) Drop(step string) {
	if u.mode == Rich {
		u.send(endMsg{step: step, ok: true, msg: dropSentinel})
	}
}

// dropSentinel marks an end that should leave no line behind.
const dropSentinel = "\x00drop"

// Skipped reports a step that needed no work.
func (u *UI) Skipped(format string, a ...any) {
	u.note("skip", "   skip: %s\n", format, a...)
}

// Warn reports something the user should know that is not fatal.
func (u *UI) Warn(format string, a ...any) {
	u.note("warn", "   warn: %s\n", format, a...)
}

// Info prints an indented note.
func (u *UI) Info(format string, a ...any) {
	u.note("info", "   %s\n", format, a...)
}

func (u *UI) note(kind, plainFormat, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if u.mode == Plain {
		fmt.Fprintf(u.out, plainFormat, msg)
		return
	}
	u.send(noteMsg{kind: kind, text: msg})
}

// Progress reports position within a step, e.g. package 7 of 24.
func (u *UI) Progress(text string, current, total int) {
	u.ProgressFor("", text, current, total)
}

// Detail updates the current activity without changing position.
func (u *UI) Detail(text string) { u.DetailFor("", text) }

// ProgressFor is Progress attributed to a named step.
func (u *UI) ProgressFor(step, text string, current, total int) {
	u.DetailFor(step, fmt.Sprintf("%s [%d/%d]", text, current, total))
}

// DetailFor is Detail attributed to a named step.
//
// The attribution is what makes concurrency legible: without it, "installing
// LyX" and "mathtools [7/35]" overwrote each other on one line during a real
// run, so the display contradicted itself every few hundred milliseconds.
func (u *UI) DetailFor(step, text string) {
	if u.mode == Plain {
		// One durable line per event rather than a redrawn block.
		fmt.Fprintf(u.out, "   %s\n", text)
		return
	}
	u.send(detailMsg{step: step, text: text})
}

// Fail reports a failure.
func (u *UI) Fail(format string, a ...any) { u.End("", false, fmt.Sprintf(format, a...)) }

// Done reports a success outside any step.
func (u *UI) Done(format string, a ...any) { u.End("", true, fmt.Sprintf(format, a...)) }

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
func (u *UI) Confirm(question, detail string, def bool) bool {
	if !u.canPrompt {
		shown := "no"
		if def {
			shown = "yes"
		}
		fmt.Fprintf(u.out, "\n ? %s\n   (not interactive - using default: %s)\n", question, shown)
		return def
	}
	if u.mode == Plain {
		return u.confirmPlain(question, detail, def)
	}

	// The display answers on a keypress and sends the result back here, which
	// keeps the installer goroutine blocked without freezing the terminal.
	reply := make(chan bool, 1)
	u.send(confirmMsg{question: question, detail: detail, def: def, reply: reply})
	return <-reply
}

func (u *UI) confirmPlain(question, detail string, def bool) bool {
	fmt.Fprintln(u.out)
	fmt.Fprintf(u.out, " ? %s\n", question)
	for _, line := range strings.Split(strings.TrimSpace(detail), "\n") {
		fmt.Fprintf(u.out, "   %s\n", line)
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

// Summary prints the closing report.
//
// The display is torn down first so the table lands in the terminal's own
// scrollback, where it survives after the program exits.
func (u *UI) Summary(rows [][]string) {
	u.Close()
	fmt.Fprintln(u.out)
	if u.mode == Plain {
		for _, r := range rows {
			fmt.Fprintf(u.out, "%s: %s\n", r[0], strings.Join(r[1:], " "))
		}
		return
	}
	fmt.Fprintln(u.out, renderTable(rows))
}

// renderTable lays out the closing summary with even columns.
func renderTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	widths := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, cell := range r {
			if i < len(widths) && lipgloss.Width(cell) > widths[i] {
				widths[i] = lipgloss.Width(cell)
			}
		}
	}
	var b strings.Builder
	for n, r := range rows {
		for i, cell := range r {
			if i >= len(widths) {
				continue
			}
			pad := strings.Repeat(" ", widths[i]-lipgloss.Width(cell))
			if n == 0 {
				cell = styleHeader.Render(cell)
			}
			b.WriteString(cell + pad + "  ")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
