// Package pkgmgr wraps the external tools that do the installing: winget,
// Chocolatey, and MiKTeX's own package manager.
package pkgmgr

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Runner executes external commands, capturing output to the log rather than
// the console. An interface so steps can be tested without running anything.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
	// RunWith is Run with per-call options: progress lines as they arrive, and
	// extra environment variables for the child and everything it spawns.
	RunWith(ctx context.Context, o RunOpts, name string, args ...string) (string, error)
}

// RunOpts are the per-call knobs. A struct rather than more parameters,
// because both are needed together exactly once and neither is common.
type RunOpts struct {
	// OnLine receives each output line as it appears, so a command that takes
	// minutes can say what it is doing while it does it.
	OnLine func(string)
	// Env adds to the child's environment rather than replacing it.
	Env []string
}

// RunAsInvoker stops Windows elevating a child that asks for the highest
// privileges available to the user.
//
// LyX's installer is built with MULTIUSER_EXECUTIONLEVEL Highest, which
// compiles to RequestExecutionLevel highest. For an administrator account that
// means Windows shows a UAC dialog when the installer starts - regardless of
// /CurrentUser, which decides where it installs but not whether it elevates.
// The result was an install that correctly landed in %LOCALAPPDATA% with HKCU
// only, and still asked for permission on the way.
//
// This is the documented application-compatibility shim for exactly that
// situation: run the child with the caller's token instead of requesting a
// higher one. It is safe here precisely because /CurrentUser makes the install
// genuinely per-user - it writes nothing that needs administrator rights - so
// there is no elevated work being silently skipped. If the per-user install
// fails anyway, the caller falls back to the ordinary machine-wide one, which
// asks for permission properly.
const RunAsInvoker = "__COMPAT_LAYER=RunAsInvoker"

// ExecRunner is the real implementation.
type ExecRunner struct {
	Log func(format string, a ...any)
}

// Run executes a command with a deadline, returning combined output.
func (r ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	return r.RunWith(ctx, RunOpts{}, name, args...)
}

// RunWith executes a command with per-call options.
func (r ExecRunner) RunWith(ctx context.Context, o RunOpts, name string, args ...string) (string, error) {
	if r.Log != nil {
		r.Log("run: %s %s", name, strings.Join(args, " "))
	}
	cmd := exec.CommandContext(ctx, name, args...)

	var buf bytes.Buffer
	var sink io.Writer = &buf
	if o.OnLine != nil {
		sink = io.MultiWriter(&buf, &lineWriter{emit: o.OnLine})
	}
	cmd.Stdout = sink
	cmd.Stderr = sink
	if len(o.Env) > 0 {
		// Added to the inherited environment, not replacing it: winget needs
		// the usual variables to find its own data.
		cmd.Env = append(os.Environ(), o.Env...)
		if r.Log != nil {
			r.Log("with environment: %s", strings.Join(o.Env, " "))
		}
	}
	hideConsole(cmd)

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

// lineWriter splits a stream into lines as it is written.
//
// It breaks on carriage returns as well as newlines: winget draws its download
// progress by rewriting one line with \r, so a newline-only split yields a
// single enormous line that only arrives once the download has finished -
// exactly the moment the progress stops being useful.
type lineWriter struct {
	emit func(string)
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexAny(w.buf, "\r\n")
		if i < 0 {
			break
		}
		if line := strings.TrimSpace(string(w.buf[:i])); line != "" {
			w.emit(line)
		}
		w.buf = w.buf[i+1:]
	}
	// Keep the tail bounded: a command that never emits a break must not grow
	// this without limit.
	if len(w.buf) > 8192 {
		w.buf = w.buf[len(w.buf)-1024:]
	}
	return len(p), nil
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
	return WingetInstallOpts(ctx, r, id, WingetOpts{})
}

// WingetOpts customises an install.
type WingetOpts struct {
	// Custom is passed through to the underlying installer.
	Custom []string
	// Env adds variables for winget and the installer it launches.
	Env []string
	// Progress receives phrases like "downloading 41.2 MB of 57.6 MB". LyX
	// takes minutes, and a motionless "installing LyX" is indistinguishable
	// from a hang - a real run sat on that line for the full 30-minute timeout.
	Progress func(string)
}

// WingetInstallOpts installs a package by exact ID with options.
func WingetInstallOpts(ctx context.Context, r Runner, id string, o WingetOpts) error {
	args := []string{"install", "--id", id, "--exact", "--silent",
		"--accept-package-agreements", "--accept-source-agreements",
		"--disable-interactivity"}
	if len(o.Custom) > 0 {
		// --custom appends to the switches winget already passes, rather than
		// replacing them as --override would.
		args = append(args, "--custom", strings.Join(o.Custom, " "))
	}

	var onLine func(string)
	if o.Progress != nil {
		onLine = func(line string) {
			if phase := wingetPhase(line); phase != "" {
				o.Progress(phase)
			}
		}
	}
	_, err := r.RunWith(ctx, RunOpts{OnLine: onLine, Env: o.Env}, "winget", args...)
	return err
}

// WingetDownload fetches a package's installer without running it, returning
// the path to the downloaded file.
//
// winget still resolves the version, picks the mirror and verifies the
// published hash - all the things worth keeping - but hands us the file instead
// of launching it. That matters because environment variables do not survive
// the hop through winget to the installer it starts: setting RunAsInvoker on
// winget left the UAC dialog exactly where it was, measured, twice. Running the
// installer ourselves is what makes the shim apply.
func WingetDownload(ctx context.Context, r Runner, id, dir string, progress func(string)) (string, error) {
	var onLine func(string)
	if progress != nil {
		onLine = func(line string) {
			if phase := wingetPhase(line); phase != "" {
				progress(phase)
			}
		}
	}
	_, err := r.RunWith(ctx, RunOpts{OnLine: onLine}, "winget", "download",
		"--id", id, "--exact",
		"--accept-package-agreements", "--accept-source-agreements",
		"--disable-interactivity", "-d", dir)
	if err != nil {
		return "", err
	}

	// Locate the result by looking rather than by parsing winget's prose: it
	// also writes a .yaml manifest beside the installer, and the file name
	// embeds a version we would otherwise have to predict.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".exe") {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("winget downloaded no installer into %s", dir)
}

// transferred matches winget's "41.2 MB / 57.6 MB" progress readout.
var transferred = regexp.MustCompile(`([\d.]+\s*[KMG]B)\s*/\s*([\d.]+\s*[KMG]B)`)

// wingetPhase turns a line of winget output into something worth showing, or
// "" for the noise. Translating rather than echoing keeps the display readable:
// winget's raw output is full of progress-bar block characters and legal notices.
func wingetPhase(line string) string {
	if m := transferred.FindStringSubmatch(line); m != nil {
		return fmt.Sprintf("downloading %s of %s", m[1], m[2])
	}
	switch {
	case strings.Contains(line, "Successfully verified installer hash"):
		return "verified the download"
	case strings.Contains(line, "Starting package install"):
		return "running the installer"
	case strings.Contains(line, "Successfully installed"):
		return "installed"
	case strings.HasPrefix(line, "Downloading"):
		return "downloading"
	case strings.Contains(line, "Found "):
		return "found it; starting"
	}
	return ""
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
	// article.cls and theorem.sty: the heb-article class needs both, and LyX
	// reports the class unavailable if they are missing when it configures.
	"latex", "tools",
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
	return SetMiKTeXAutoInstall(ctx, r, binDir, true)
}

// SetMiKTeXAutoInstall turns on-demand package installation on or off.
//
// It has to be switchable because leaving it on makes installing LyX roughly
// seven minutes slower. LyX's configure runs latex ~122 times to test which
// document classes work; with AutoInstall on, every one of those runs that
// touches something missing sends MiKTeX off to resolve it, and miktex.log
// shows the result - latex is the parent of 122 of the 130 miktex-fc-cache
// invocations, each taking about three and a half seconds.
//
// Turning it off for the duration of the LyX install costs nothing: LyX's own
// verdict about document classes is recomputed afterwards by the reconfigure
// step, which runs with it back on.
func SetMiKTeXAutoInstall(ctx context.Context, r Runner, binDir string, on bool) error {
	value := "0"
	if on {
		value = "1"
	}
	_, err := r.Run(ctx, binDir+`\initexmf.exe`, "--set-config-value", "[MPM]AutoInstall="+value)
	return err
}

// WarmMiKTeX builds the caches that a fresh MiKTeX otherwise builds lazily,
// once each, at the worst possible moment.
//
// Measured on a clean machine: installing LyX straight after MiKTeX took 8m41s,
// and miktex.log shows why - LyX's configure invoked miktex-fc-cache 118 times,
// each rebuilding the font-config cache from nothing at roughly 3.5 seconds a
// go. The same LyX install against an already-used MiKTeX took 35 seconds, and
// a warm miktex-fc-cache returns in 0.73s.
//
// So the cost is not the download (11s), not Defender scanning the 3,576 files
// LyX extracts, and not LyX's configure itself (4 LaTeX runs, 21s) - it is one
// cache being rebuilt over and over. Building it once here needs no
// administrator rights, which matters because the alternative fix, Defender
// exclusions, does.
func WarmMiKTeX(ctx context.Context, r Runner, binDir string, progress func(string)) {
	steps := []struct {
		name string
		exe  string
		args []string
	}{
		{"font cache", "miktex-fc-cache.exe", []string{"--miktex-disable-maintenance"}},
		{"file name database", "initexmf.exe", []string{"--update-fndb"}},
	}
	for _, s := range steps {
		if progress != nil {
			progress("preparing the " + s.name)
		}
		each, cancel := context.WithTimeout(ctx, 10*time.Minute)
		_, err := r.Run(each, filepath.Join(binDir, s.exe), s.args...)
		cancel()
		if err != nil {
			// Warming is an optimisation; a failure costs time, not correctness.
			continue
		}
	}
}

// KpsewhichFinds reports whether TeX can resolve a file, which is how the
// doctor checks that culmus.sty and friends are actually reachable.
func KpsewhichFinds(ctx context.Context, r Runner, binDir, file string) bool {
	out, err := r.Run(ctx, binDir+`\kpsewhich.exe`, file)
	return err == nil && strings.TrimSpace(out) != ""
}
