//go:build windows

package pkgmgr

import (
	"os/exec"
	"syscall"
)

// createNoWindow keeps a console child from borrowing our console.
//
// Capturing stdout and stderr is not enough on its own: a grandchild inherits
// the console handle and writes straight to it, bypassing the pipes entirely.
// That is how pages of METAFONT and pdflatex output ended up interleaved with
// the installer's own display during a real run.
const createNoWindow = 0x08000000

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
