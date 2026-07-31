//go:build windows

package pkgmgr

import (
	"os/exec"
	"syscall"
)

// createNewConsole gives a child its own console window, hidden.
//
// winget's own output arrives through our pipes and is unaffected. Its
// children are the problem: MiKTeX's installer writes to the console device
// directly, and those writes landed in the middle of the live progress block,
// corrupting pterm's cursor tracking so the block scrolled instead of
// redrawing. CREATE_NO_WINDOW was tried first and does not stop it - a probe
// with a grandchild writing to CONOUT$ showed the write still succeeding.
//
// Giving the child its own hidden console leaves the real one to the display.
const createNewConsole = 0x00000010

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNewConsole}
}
