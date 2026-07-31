// Package winenv holds the Windows facts the installer depends on: where LyX
// and TeX live, whether we are elevated, and whether the profile path is safe.
package winenv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LyX describes an installation found on disk.
type LyX struct {
	Root    string
	Exe     string
	Version string // e.g. "2.4.4"
	Series  string // e.g. "2.4"
}

// UserDirName is the %APPDATA% folder LyX keeps settings in.
func (l LyX) UserDirName() string { return "LyX" + l.Series }

// BindSeries picks which bundled shortcut file suits this LyX.
//
// Kali publishes builds for 2.3 (bind Format 4) and 2.4 (Format 5) only.
// Anything newer - LyX 2.5 shipped in February 2026 - gets the 2.4 file, which
// is safe: LyX detects a format mismatch and runs the file through prefs2prefs
// automatically (see KeyMap::read in the LyX source), converting on load rather
// than rejecting it.
//
// The comparison is numeric. The PowerShell original compared version strings,
// so a future LyX 2.10 would have been handed the 2.3 file.
func (l LyX) BindSeries() (series string, exact bool) {
	major, minor := l.parts()
	if major < 2 || (major == 2 && minor < 4) {
		return "2.3", major == 2 && minor == 3
	}
	return "2.4", major == 2 && minor == 4
}

// WantsMutedColors reports whether this LyX needs the \set_color overrides.
// LyX 2.4 introduced in-editor colours the guide calls unreadable (p.46).
func (l LyX) WantsMutedColors() bool {
	major, minor := l.parts()
	return major > 2 || (major == 2 && minor >= 4)
}

func (l LyX) parts() (major, minor int) {
	fmt.Sscanf(l.Series, "%d.%d", &major, &minor)
	return
}

// FindLyX locates LyX, or reports false.
//
// Every candidate directory is probed before use. The PowerShell original
// called os.listdir on four hard-coded paths, one of which frequently does not
// exist, and threw before reaching the one that did.
func FindLyX() (LyX, bool) {
	var bases []string
	if p := os.Getenv("LOCALAPPDATA"); p != "" {
		bases = append(bases, filepath.Join(p, "Programs"), p)
	}
	if p := os.Getenv("ProgramFiles"); p != "" {
		bases = append(bases, p)
	}
	if p := os.Getenv("ProgramFiles(x86)"); p != "" {
		bases = append(bases, p)
	}

	var found []LyX
	for _, base := range bases {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue // a missing directory is not an error
		}
		for _, e := range entries {
			if !e.IsDir() || !strings.HasPrefix(e.Name(), "LyX") {
				continue
			}
			exe := filepath.Join(base, e.Name(), "bin", "LyX.exe")
			if _, err := os.Stat(exe); err != nil {
				continue
			}
			ver := fileVersion(exe)
			if ver == "" {
				ver = versionFromName(e.Name())
			}
			if ver == "" {
				continue
			}
			var maj, min int
			fmt.Sscanf(ver, "%d.%d", &maj, &min)
			found = append(found, LyX{
				Root:    filepath.Join(base, e.Name()),
				Exe:     exe,
				Version: ver,
				Series:  fmt.Sprintf("%d.%d", maj, min),
			})
		}
	}
	if len(found) == 0 {
		return LyX{}, false
	}
	// Newest wins if several are installed.
	sort.Slice(found, func(i, j int) bool { return found[i].Version > found[j].Version })
	return found[0], true
}

func versionFromName(name string) string {
	var maj, min, patch int
	if n, _ := fmt.Sscanf(name, "LyX %d.%d.%d", &maj, &min, &patch); n >= 2 {
		return fmt.Sprintf("%d.%d.%d", maj, min, patch)
	}
	return ""
}

// FindLyXUserDir returns %APPDATA%\LyX2.x, preferring the installed version.
func FindLyXUserDir(l LyX) (string, bool) {
	roaming := os.Getenv("APPDATA")
	if roaming == "" {
		return "", false
	}
	if l.Series != "" {
		exact := filepath.Join(roaming, l.UserDirName())
		if st, err := os.Stat(exact); err == nil && st.IsDir() {
			return exact, true
		}
	}
	entries, err := os.ReadDir(roaming)
	if err != nil {
		return "", false
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "LyX") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", false
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return filepath.Join(roaming, names[0]), true
}

// TeX describes a TeX distribution.
type TeX struct {
	Distro string // "miktex" or "texlive"
	BinDir string
}

// FindMiKTeX locates a MiKTeX installation.
func FindMiKTeX() (TeX, bool) {
	var candidates []string
	if p, err := exec.LookPath("initexmf"); err == nil {
		candidates = append(candidates, filepath.Dir(p))
	}
	if p := os.Getenv("LOCALAPPDATA"); p != "" {
		candidates = append(candidates,
			filepath.Join(p, "Programs", "MiKTeX", "miktex", "bin", "x64"),
			filepath.Join(p, "Programs", "MiKTeX", "miktex", "bin"))
	}
	candidates = append(candidates,
		`C:\Program Files\MiKTeX\miktex\bin\x64`,
		`C:\Program Files\MiKTeX 2.9\miktex\bin\x64`,
		`C:\Program Files (x86)\MiKTeX 2.9\miktex\bin\x64`)

	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "pdflatex.exe")); err == nil {
			return TeX{Distro: "miktex", BinDir: dir}, true
		}
	}
	return TeX{}, false
}

// FindTeXLive locates a TeX Live installation.
//
// The bin directory is searched for pdflatex rather than asserted to hold
// exactly one entry, which is what the PowerShell original did - it raised a
// bare AssertionError on any tree upgraded across releases.
func FindTeXLive() (TeX, bool) {
	if p, err := exec.LookPath("tlmgr"); err == nil {
		dir := filepath.Dir(p)
		if _, err := os.Stat(filepath.Join(dir, "pdflatex.exe")); err == nil {
			return TeX{Distro: "texlive", BinDir: dir}, true
		}
	}
	years, err := os.ReadDir(`C:\texlive`)
	if err != nil {
		return TeX{}, false
	}
	var names []string
	for _, y := range years {
		if y.IsDir() && len(y.Name()) == 4 {
			names = append(names, y.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, y := range names {
		binRoot := filepath.Join(`C:\texlive`, y, "bin")
		platforms, err := os.ReadDir(binRoot)
		if err != nil {
			continue
		}
		for _, p := range platforms {
			dir := filepath.Join(binRoot, p.Name())
			if _, err := os.Stat(filepath.Join(dir, "pdflatex.exe")); err == nil {
				return TeX{Distro: "texlive", BinDir: dir}, true
			}
		}
	}
	return TeX{}, false
}

// FindTeX returns whichever distribution is installed, MiKTeX first.
func FindTeX() (TeX, bool) {
	if t, ok := FindMiKTeX(); ok {
		return t, true
	}
	return FindTeXLive()
}

// HasHebrew reports whether the text contains Hebrew.
//
// Go source is UTF-8 by definition, so the encoding hazard that silently broke
// the PowerShell version - literal Hebrew in a file with no byte-order mark,
// read as ANSI by Windows PowerShell 5.1 - cannot occur here.
func HasHebrew(s string) bool {
	for _, r := range s {
		if (r >= 0x0590 && r <= 0x05FF) || (r >= 0xFB1D && r <= 0xFB4F) {
			return true
		}
	}
	return false
}

// ProfileHasHebrew reports whether the user profile path contains Hebrew,
// which the MadLyX guide names as the leading cause of failed PDF export.
func ProfileHasHebrew() (string, bool) {
	home, _ := os.UserHomeDir()
	return home, HasHebrew(home)
}

// WaitFor polls until fn succeeds or the timeout expires.
//
// winget returns as soon as it has handed off to the underlying installer.
// LyX ships as an NSIS package that keeps writing files afterwards, so a single
// immediate check reports it missing and sends the caller down a pointless
// fallback path.
func WaitFor[T any](timeout time.Duration, interval time.Duration, fn func() (T, bool)) (T, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if v, ok := fn(); ok {
			return v, true
		}
		if time.Now().After(deadline) {
			var zero T
			return zero, false
		}
		time.Sleep(interval)
	}
}
