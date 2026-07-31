//go:build windows

package steps

import (
	"os/exec"
	"syscall"
)

// detachedProcess stops a child - and anything it spawns - from inheriting our
// console.
//
// Without it, running LyX in batch mode dumps its own warnings and the whole of
// pdflatex's output straight into the user's terminal, past the installer's own
// display. A real run produced pages of METAFONT font generation interleaved
// with the live progress block.
//
// CombinedOutput alone is not enough: a grandchild writes to the inherited
// console rather than to the pipes we handed the child.
//
// CREATE_NO_WINDOW was tried first and did not work. It is documented as
// applying to console applications - LyX is a GUI application, so the flag is
// ignored, LyX inherits our console anyway, and the pdflatex it spawns writes
// straight to it. DETACHED_PROCESS is unconditional: the child gets no console
// whatever its subsystem. Our own stdout and stderr pipes are unaffected,
// because they are explicit handles rather than the console.
//
// The two flags are mutually exclusive, so console tools that merely need their
// window suppressed use CREATE_NO_WINDOW instead - see internal/pkgmgr.
const detachedProcess = 0x00000008

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess,
	}
}
