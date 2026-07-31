//go:build windows

package steps

import (
	"os/exec"
	"syscall"
)

// createNoWindow stops a child - and anything it spawns - from inheriting or
// opening a console.
//
// Without it, running LyX in batch mode dumps its own warnings and the whole of
// pdflatex's output straight into the user's terminal, past the installer's own
// output. A real run produced pages of METAFONT font generation and a
// "Document class not available" warning that looked like a failure but was
// not: MiKTeX was installing the missing file on demand at that moment, and the
// compile succeeded.
//
// CombinedOutput alone is not enough, because grandchildren write to the
// inherited console rather than to the pipes we handed the child.
const createNoWindow = 0x08000000

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
