// Command madlyx-spike is a throwaway probe, not the installer.
//
// It exists to answer one question that could kill the Go port: does Windows
// SmartScreen block an unsigned, freshly published binary downloaded from a
// GitHub release? Reputation is keyed on how many users have run a given file
// hash, so a brand-new release is the worst case and the only honest test.
//
// While it is here it also demonstrates the two things that make Go a better
// fit than PowerShell for this job: the whole payload compiles into the binary,
// and the detection logic ports across almost unchanged.
package main

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// The entire MadLyX payload, compiled in. No zipball, no extraction, no
// mark-of-the-web on the extracted files, nothing to wait for a CDN to serve.
//
//go:embed payload
var payload embed.FS

// Build metadata, injected with -ldflags at release time.
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	fmt.Println()
	fmt.Println("  MadLyX spike -- SmartScreen probe")
	fmt.Println("  ---------------------------------")
	fmt.Printf("  version %s (%s), %s, %s/%s\n\n",
		version, commit, runtime.Version(), runtime.GOOS, runtime.GOARCH)

	reportEmbeddedPayload()
	reportDetection()

	fmt.Println("\n  If you are reading this, Windows let an unsigned binary run.")
	fmt.Println()
}

// reportEmbeddedPayload proves the payload survived compilation intact.
func reportEmbeddedPayload() {
	fmt.Println("  Embedded payload")
	var total int
	var files []string
	err := fs_walk("payload", func(path string, size int) {
		files = append(files, fmt.Sprintf("    %-46s %8d bytes", strings.TrimPrefix(path, "payload/"), size))
		total += size
	})
	if err != nil {
		fmt.Printf("    could not read embedded payload: %v\n", err)
		return
	}
	sort.Strings(files)
	for _, f := range files {
		fmt.Println(f)
	}
	fmt.Printf("    %-46s %8d bytes total\n", fmt.Sprintf("(%d files)", len(files)), total)
}

func fs_walk(dir string, visit func(path string, size int)) error {
	entries, err := payload.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		p := dir + "/" + e.Name()
		if e.IsDir() {
			if err := fs_walk(p, visit); err != nil {
				return err
			}
			continue
		}
		data, err := payload.ReadFile(p)
		if err != nil {
			return err
		}
		visit(p, len(data))
	}
	return nil
}

// reportDetection ports the LyX and TeX discovery from lib/LyX.ps1 and
// lib/TeX.ps1 to show how little changes in translation.
func reportDetection() {
	fmt.Println("\n  Detection (ported from lib/LyX.ps1 and lib/TeX.ps1)")

	if root, ok := findLyX(); ok {
		fmt.Printf("    LyX     : %s\n", root)
	} else {
		fmt.Println("    LyX     : not installed")
	}

	if bin, ok := findTeX(); ok {
		fmt.Printf("    TeX     : %s\n", bin)
	} else {
		fmt.Println("    TeX     : not installed")
	}

	profile, _ := os.UserHomeDir()
	fmt.Printf("    Profile : %s (Hebrew: %v)\n", profile, hasHebrew(profile))
}

func findLyX() (string, bool) {
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
	for _, base := range bases {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue // missing directory is not an error, unlike the PowerShell original
		}
		for _, e := range entries {
			if !e.IsDir() || !strings.HasPrefix(e.Name(), "LyX") {
				continue
			}
			exe := filepath.Join(base, e.Name(), "bin", "LyX.exe")
			if _, err := os.Stat(exe); err == nil {
				return filepath.Join(base, e.Name()), true
			}
		}
	}
	return "", false
}

func findTeX() (string, bool) {
	candidates := []string{}
	if p := os.Getenv("LOCALAPPDATA"); p != "" {
		candidates = append(candidates,
			filepath.Join(p, "Programs", "MiKTeX", "miktex", "bin", "x64"))
	}
	candidates = append(candidates,
		`C:\Program Files\MiKTeX\miktex\bin\x64`,
		`C:\Program Files\MiKTeX 2.9\miktex\bin\x64`)

	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "pdflatex.exe")); err == nil {
			return dir, true
		}
	}

	// TeX Live: newest year first, and pick the platform dir that actually has
	// pdflatex rather than asserting there is exactly one, as the original did.
	years, err := os.ReadDir(`C:\texlive`)
	if err != nil {
		return "", false
	}
	sort.Slice(years, func(i, j int) bool { return years[i].Name() > years[j].Name() })
	for _, y := range years {
		binRoot := filepath.Join(`C:\texlive`, y.Name(), "bin")
		platforms, err := os.ReadDir(binRoot)
		if err != nil {
			continue
		}
		for _, p := range platforms {
			dir := filepath.Join(binRoot, p.Name())
			if _, err := os.Stat(filepath.Join(dir, "pdflatex.exe")); err == nil {
				return dir, true
			}
		}
	}
	return "", false
}

// hasHebrew mirrors Test-HasHebrew. In Go the source is UTF-8 by definition, so
// the encoding hazard that silently broke the PowerShell version cannot occur.
func hasHebrew(s string) bool {
	for _, r := range s {
		if (r >= 0x0590 && r <= 0x05FF) || (r >= 0xFB1D && r <= 0xFB4F) {
			return true
		}
	}
	return false
}
