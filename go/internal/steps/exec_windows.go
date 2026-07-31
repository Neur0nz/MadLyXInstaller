//go:build windows

package steps

import (
	"os/exec"
	"syscall"
)

// createNewConsole gives a child its own console window, hidden.
//
// Two earlier attempts failed, both for the same underlying reason. Capturing
// stdout and stderr does nothing, because LyX writes to the console device
// rather than to the pipes we hand it - measured: running the real smoke test
// three ways returned zero captured bytes every time, while pages of pdflatex
// and METAFONT output appeared on screen. CREATE_NO_WINDOW does not help
// either: a probe that had a grandchild write to CONOUT$ showed the write
// still succeeding, landing in our console.
//
// Suppressing the console is the wrong goal anyway. What is needed is a
// console that is not ours: children and grandchildren write into a buffer
// nobody sees, and the live progress display keeps sole ownership of the real
// one. That last part matters as much as the tidiness - foreign writes landing
// mid-frame corrupted pterm's cursor tracking, so the status block scrolled
// down the screen instead of redrawing in place.
//
// Diagnostics do not depend on this output: the compile is diagnosed from the
// LaTeX .log file, which is more reliable than scraped console text.
const createNewConsole = 0x00000010

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true, // SW_HIDE, so the new console never appears
		CreationFlags: createNewConsole,
	}
}
