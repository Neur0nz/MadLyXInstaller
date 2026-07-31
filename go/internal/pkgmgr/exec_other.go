//go:build !windows

package pkgmgr

import "os/exec"

// hideConsole is a no-op away from Windows: there is no console to inherit.
// The package still builds everywhere so the pure-logic tests can run anywhere.
func hideConsole(*exec.Cmd) {}
