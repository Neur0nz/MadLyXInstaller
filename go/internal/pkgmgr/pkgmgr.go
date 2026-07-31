// Package pkgmgr wraps the external tools that do the installing: winget,
// Chocolatey, and MiKTeX's own package manager.
package pkgmgr

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runner executes external commands, capturing output to the log rather than
// the console. An interface so steps can be tested without running anything.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// ExecRunner is the real implementation.
type ExecRunner struct {
	Log func(format string, a ...any)
}

// Run executes a command with a deadline, returning combined output.
func (r ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	if r.Log != nil {
		r.Log("run: %s %s", name, strings.Join(args, " "))
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if r.Log != nil && out != "" {
		r.Log("output: %s", strings.TrimSpace(out))
	}
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%s timed out", name)
	}
	return out, err
}

// Available reports whether a command is on PATH.
func Available(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// WingetInstall installs a package by exact ID.
//
// --exact with an explicit --id is the only form safe to run unattended: a
// bare query is matched fuzzily by the Microsoft Store source, and winget's
// query argument is variadic, so `winget install python 3` installs two
// packages. The accept flags matter too - on a machine that has never run
// winget, the source-agreement prompt blocks forever.
func WingetInstall(ctx context.Context, r Runner, id string) error {
	_, err := r.Run(ctx, "winget", "install",
		"--id", id, "--exact", "--silent",
		"--accept-package-agreements", "--accept-source-agreements")
	return err
}

// WingetUninstall removes a package by exact ID.
func WingetUninstall(ctx context.Context, r Runner, id string) error {
	_, err := r.Run(ctx, "winget", "uninstall", "--id", id, "--exact", "--silent")
	return err
}

// ChocoInstall is the fallback when winget cannot deliver.
func ChocoInstall(ctx context.Context, r Runner, name string) error {
	_, err := r.Run(ctx, "choco", "install", name, "-y", "--no-progress")
	return err
}

// HebrewPackages are the LaTeX packages a Hebrew maths document needs.
//
// babel-hebrew and culmus are the Hebrew core; preview and dvipng back LyX's
// instant preview; the middle group is what the MadLyX templates and macros
// pull in.
//
// The last group is optional extras, commented out in the shipped preamble but
// installed anyway so uncommenting a line just works. Every one of them was
// compiled against a Hebrew document first - RTL handling conflicts with a
// fair number of packages, and guessing is unreliable. algorithmicx is
// deliberately absent: it is the one that failed.
var HebrewPackages = []string{
	// Hebrew core
	"babel-hebrew", "hebrew-fonts", "culmus",
	// LyX instant preview
	"preview", "dvipng",
	// used by the MadLyX templates and macros
	"mathtools", "stmaryrd", "relsize", "cancel", "esint",
	"mathdots", "mhchem", "undertilde", "stackrel",
	"xcolor", "listings", "multicol", "hyperref", "atbegshi",
	"amsmath", "amsfonts", "dsfont", "wasysym",
	// optional extras, verified against Hebrew
	"physics", "siunitx", "braket", "esdiff", "mathrsfs", "bbm",
	"tcolorbox", "cleveref", "microtype", "enumitem",
	"booktabs", "tabularx", "caption", "subcaption", "wrapfig",
	"pgf", "pgfplots", "algorithm2e",
}

// InstallTeXPackages installs packages one at a time so a single unavailable
// name cannot abort the batch, reporting progress as it goes. The PowerShell
// version printed one line and then nothing for the whole loop.
func InstallTeXPackages(ctx context.Context, r Runner, distro, binDir string,
	progress func(name string, i, total int)) error {

	total := len(HebrewPackages)
	var failed []string

	for i, pkg := range HebrewPackages {
		if progress != nil {
			progress(pkg, i+1, total)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var err error
		each, cancel := context.WithTimeout(ctx, 5*time.Minute)
		if distro == "miktex" {
			_, err = r.Run(each, binDir+`\mpm.exe`, "--install", pkg)
		} else {
			_, err = r.Run(each, binDir+`\tlmgr.bat`, "install", pkg)
		}
		cancel()
		if err != nil {
			failed = append(failed, pkg)
		}
	}

	// On MiKTeX a missing package installs itself on first use, so this is not
	// fatal. Report it rather than hiding it.
	if len(failed) > 0 {
		return fmt.Errorf("%d package(s) did not install: %s", len(failed), strings.Join(failed, ", "))
	}
	return nil
}

// EnableMiKTeXAutoInstall turns on installing missing packages on demand,
// which is the whole reason MiKTeX is preferred over TeX Live here: it removes
// the guide's entire "installing new packages" chapter.
func EnableMiKTeXAutoInstall(ctx context.Context, r Runner, binDir string) error {
	_, err := r.Run(ctx, binDir+`\initexmf.exe`, "--set-config-value", "[MPM]AutoInstall=1")
	return err
}

// KpsewhichFinds reports whether TeX can resolve a file, which is how the
// doctor checks that culmus.sty and friends are actually reachable.
func KpsewhichFinds(ctx context.Context, r Runner, binDir, file string) bool {
	out, err := r.Run(ctx, binDir+`\kpsewhich.exe`, file)
	return err == nil && strings.TrimSpace(out) != ""
}
