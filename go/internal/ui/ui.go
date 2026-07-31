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
// Several steps run at once, so the live display gives each one its own line.
// A single shared line was tried first and it lied: during a real install
// "installing LyX" and "mathtools [7/35]" overwrote each other continuously.
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
	// Rich renders a live status block and colour. Chosen when stdout is a terminal.
	Rich Mode = iota
	// Plain emits one durable line per event: no colour, no redraws, no ANSI.
	Plain
)

// redrawInterval is how often the live block is repainted. Fast enough that
// the spinner reads as motion, slow enough to be invisible in CPU terms.
const redrawInterval = 125 * time.Millisecond

// spinnerFrames animate the header. Braille cells advance smoothly and occupy
// one column in every terminal we target.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// task is one step currently running.
type task struct {
	started time.Time
	detail  string
}

// UI carries terminal capabilities and the assumptions that follow from them.
type UI struct {
	mode      Mode
	canPrompt bool
	out       io.Writer
	width     int
	// paint gates the live redraw. Only a real terminal gets one: the area
	// printer writes to the process's stdout rather than u.out, so leaving it
	// on under test would scribble over the test runner's own output.
	paint bool

	mu      sync.Mutex
	running map[string]*task
	order   []string // display order: the order steps began
	done    int
	total   int
	tick    int
	area    *pterm.AreaPrinter
	stop    chan struct{} // closed to end the redraw loop
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
	u := NewFor(os.Stdout, mode, stdoutTTY && stdinTTY && !assumeYes)
	u.paint = stdoutTTY
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 20 {
		u.width = w
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
	return &UI{
		mode:      mode,
		canPrompt: canPrompt,
		out:       out,
		width:     100,
		running:   map[string]*task{},
	}
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
	u.clearLocked()
	u.mu.Unlock()
	if u.mode == Plain {
		fmt.Fprintf(u.out, "\n== %s\n", title)
		return
	}
	pterm.DefaultSection.Println(title)
	u.mu.Lock()
	u.renderLocked()
	u.mu.Unlock()
}

// Overall records how much of the plan has settled.
func (u *UI) Overall(done, total int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.done, u.total = done, total
	u.renderLocked()
}

// Begin announces that a step has started. Several may be active at once.
func (u *UI) Begin(step string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, seen := u.running[step]; !seen {
		u.order = append(u.order, step)
	}
	u.running[step] = &task{started: time.Now()}

	if u.mode == Plain {
		fmt.Fprintf(u.out, "-> %s\n", step)
		return
	}
	u.startLoopLocked()
	u.renderLocked()
}

// End reports that a step finished. ok=false renders it as a failure.
func (u *UI) End(step string, ok bool, msg string) {
	u.mu.Lock()
	t, wasRunning := u.running[step]
	elapsed := ""
	if wasRunning {
		elapsed = " " + humanDuration(time.Since(t.started))
	}
	u.forgetLocked(step)
	// Erase the live block before printing, so the completion line is not
	// overwritten by the next repaint.
	u.clearLocked()
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

	// Anything still running keeps its line.
	u.mu.Lock()
	u.renderLocked()
	u.mu.Unlock()
}

// Drop removes a step from the live block without announcing anything.
//
// Used when an optional step fails: the caller follows with a warning, and
// ending it as a success first printed "SUCCESS Defender exclusions" directly
// above "WARNING Defender exclusions did not complete", which read as both at
// once.
func (u *UI) Drop(step string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.forgetLocked(step)
	u.renderLocked()
}

// Skipped reports a step that needed no work.
func (u *UI) Skipped(format string, a ...any) {
	u.durable(func(msg string) { pterm.Info.Println(msg) }, "   skip: %s\n", format, a...)
}

// Warn reports something the user should know that is not fatal.
func (u *UI) Warn(format string, a ...any) {
	u.durable(func(msg string) { pterm.Warning.Println(msg) }, "   warn: %s\n", format, a...)
}

// durable prints a line that stays on screen, around the live block.
func (u *UI) durable(rich func(string), plainFormat, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	u.mu.Lock()
	u.clearLocked()
	u.mu.Unlock()
	if u.mode == Plain {
		fmt.Fprintf(u.out, plainFormat, msg)
	} else {
		rich(msg)
	}
	u.mu.Lock()
	u.renderLocked()
	u.mu.Unlock()
}

// Progress reports position within a step, e.g. package 7 of 24.
func (u *UI) Progress(text string, current, total int) {
	u.ProgressFor(u.soleRunner(), text, current, total)
}

// Detail updates the current activity without changing position.
func (u *UI) Detail(text string) { u.DetailFor(u.soleRunner(), text) }

// ProgressFor is Progress attributed to a named step.
func (u *UI) ProgressFor(step, text string, current, total int) {
	u.DetailFor(step, fmt.Sprintf("%s [%d/%d]", text, current, total))
}

// DetailFor is Detail attributed to a named step.
func (u *UI) DetailFor(step, text string) {
	if u.mode == Plain {
		// One durable line per event rather than a redrawn block.
		fmt.Fprintf(u.out, "   %s\n", text)
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if t, ok := u.running[step]; ok {
		t.detail = text
		u.renderLocked()
	}
}

// soleRunner names the running step when there is exactly one, so the
// unattributed Detail and Progress still land somewhere sensible. With several
// running there is no honest answer, and guessing is what produced the
// contradictory status line this display replaced.
func (u *UI) soleRunner() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.order) == 1 {
		return u.order[0]
	}
	return ""
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
	u.durable(func(m string) { fmt.Fprintf(u.out, "   %s\n", pterm.Gray(m)) }, "   %s\n", "%s", msg)
}

// startLoopLocked begins repainting so elapsed times advance and the spinner
// turns even while a step sits inside a ten-minute winget call.
func (u *UI) startLoopLocked() {
	if u.stop != nil {
		return
	}
	stop := make(chan struct{})
	u.stop = stop
	go func() {
		ticker := time.NewTicker(redrawInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				u.mu.Lock()
				u.tick++
				u.renderLocked()
				u.mu.Unlock()
			}
		}
	}()
}

// forgetLocked removes a finished step, stopping the repaint loop when the
// last one goes. Callers must hold u.mu.
func (u *UI) forgetLocked(step string) {
	delete(u.running, step)
	for i, n := range u.order {
		if n == step {
			u.order = append(u.order[:i], u.order[i+1:]...)
			break
		}
	}
	if len(u.order) == 0 && u.stop != nil {
		close(u.stop)
		u.stop = nil
	}
}

// renderLocked repaints the live block. Callers must hold u.mu.
func (u *UI) renderLocked() {
	if u.mode != Rich {
		return
	}
	if len(u.order) == 0 {
		u.clearLocked()
		return
	}
	if !u.paint {
		return
	}
	if u.area == nil {
		a, err := pterm.DefaultArea.WithRemoveWhenDone(true).Start()
		if err != nil {
			return
		}
		u.area = a
	}
	u.area.Update(u.frameLocked())
}

// clearLocked erases the live block. Callers must hold u.mu.
func (u *UI) clearLocked() {
	if u.area != nil {
		_ = u.area.Stop()
		u.area = nil
	}
}

// frameLocked composes the block: a header with overall position, then one
// line per running step. Callers must hold u.mu.
func (u *UI) frameLocked() string {
	glyph := spinnerFrames[u.tick%len(spinnerFrames)]
	head := fmt.Sprintf("%s  %d/%d steps done", glyph, u.done, u.total)
	if len(u.order) > 1 {
		head += fmt.Sprintf("  ·  %d running", len(u.order))
	}

	nameWidth := 0
	for _, n := range u.order {
		if len(n) > nameWidth {
			nameWidth = len(n)
		}
	}
	if nameWidth > 26 {
		nameWidth = 26
	}

	var b strings.Builder
	b.WriteString(pterm.Cyan(head))
	for _, n := range u.order {
		t := u.running[n]
		elapsed := humanDuration(time.Since(t.started))
		name := fit(n, nameWidth)

		// Compose in plain text so the width arithmetic is not thrown off by
		// the escape sequences colour adds, and leave two columns spare.
		//
		// The detail is deliberately not padded out to the full width. Padding
		// made every line exactly as wide as the terminal, and a line that
		// exactly fills a terminal wraps - which cost the redraw a line and
		// made the whole block scroll down the screen instead of repainting.
		fixed := 3 + len([]rune(name)) + 2 + 1 + len(elapsed)
		detail := trunc(t.detail, u.width-fixed-2)

		b.WriteString("\n   " + name + "  " + pterm.Gray(detail))
		b.WriteString(" " + pterm.Gray(elapsed))
	}
	return b.String()
}

// fit pads or truncates to exactly n columns, so the name column lines up.
func fit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) > n {
		return trunc(s, n)
	}
	return s + strings.Repeat(" ", n-len(r))
}

// trunc shortens to at most n columns without padding.
func trunc(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
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

	// Steps that prompt never run alongside others, so the block can be erased
	// outright rather than restored afterwards.
	u.mu.Lock()
	u.clearLocked()
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
	u.clearLocked()
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
