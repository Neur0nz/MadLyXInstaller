//go:build windows

package steps

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Neur0nz/MadLyXInstaller/go/internal/payload"
	"github.com/Neur0nz/MadLyXInstaller/go/internal/step"
	"github.com/Neur0nz/MadLyXInstaller/go/internal/winenv"
	"github.com/Neur0nz/MadLyXInstaller/go/internal/winsys"
)

// languageToggle offers the highest-value change in the whole installer.
//
// The guide's standing instruction (p.17) is that Windows must stay on English,
// because LyX types Hebrew through its own keyboard map and every shortcut
// breaks when Windows flips. Alt+Shift is trivially easy to hit while reaching
// for Alt-based shortcuts, so the instruction is easier to follow if the key
// simply stops doing that.
func languageToggle() *step.Step {
	return &step.Step{
		ID:       "altshift",
		Name:     "Alt+Shift language switch",
		Optional: true,
		Check: func(c *step.Context) (step.State, error) {
			off, err := winsys.LanguageToggleDisabled()
			if err != nil {
				return step.Unknown, err
			}
			if off {
				return step.Satisfied, nil
			}
			return step.Pending, nil
		},
		Apply: func(c *step.Context) error {
			detail := "Windows switches input language with Alt+Shift. LyX types Hebrew through\n" +
				"its own keyboard map (F12), so Windows must stay on English - if it flips,\n" +
				"every LyX shortcut stops working.\n\n" +
				"Win+Space and the taskbar picker keep working, so Hebrew stays available.\n" +
				"Reversible any time in Windows Settings."
			if !winsys.HebrewInputInstalled() {
				detail += "\n\nNo Hebrew input language is installed, so this is precautionary."
			}
			// Defaults to no: a piped or unattended run must never silently
			// edit the registry.
			if !c.UI.Confirm("Disable the Alt+Shift language switch?", detail, false) {
				return fmt.Errorf("declined")
			}
			if err := winsys.DisableLanguageToggle(); err != nil {
				return err
			}
			c.UI.Detail("takes effect for newly started programs; sign out to apply everywhere")
			return nil
		},
		Undo: func(c *step.Context) error { return winsys.RestoreLanguageToggle() },
	}
}

// defenderExclusions speeds up compiling. LaTeX creates and deletes many small
// files per run, and MiKTeX writes thousands when installing packages.
func defenderExclusions() *step.Step {
	return &step.Step{
		ID:       "defender",
		Name:     "Defender exclusions",
		Needs:    []string{"tex"},
		Optional: true,
		Check: func(c *step.Context) (step.State, error) {
			t, ok := step.Get[winenv.TeX](c, keyTeX)
			if !ok {
				return step.Unknown, nil
			}
			existing, err := winsys.DefenderExclusions()
			if err != nil {
				return step.Unknown, nil // Defender may be managed or absent
			}
			for _, e := range existing {
				if e == texRoot(t) {
					return step.Satisfied, nil
				}
			}
			return step.Pending, nil
		},
		Apply: func(c *step.Context) error {
			if !winsys.IsAdmin() {
				return fmt.Errorf("needs administrator rights")
			}
			t, _ := step.Get[winenv.TeX](c, keyTeX)
			l, _ := step.Get[winenv.LyX](c, keyLyX)

			paths := []string{texRoot(t)}
			if l.Root != "" {
				paths = append(paths, l.Root)
			}
			detail := "LaTeX creates and deletes many small files on every compile, and MiKTeX\n" +
				"writes thousands when installing packages. Excluding these folders from\n" +
				"real-time scanning makes compiling noticeably faster.\n\nFolders:\n"
			for _, p := range paths {
				detail += "  " + p + "\n"
			}
			detail += "\nRemovable in Windows Security > Virus & threat protection > Exclusions."

			if !c.UI.Confirm("Add Windows Defender exclusions?", detail, false) {
				return fmt.Errorf("declined")
			}
			return winsys.AddDefenderExclusions(paths)
		},
		Undo: func(c *step.Context) error {
			t, ok := step.Get[winenv.TeX](c, keyTeX)
			if !ok {
				return nil
			}
			return winsys.RemoveDefenderExclusions([]string{texRoot(t)})
		},
	}
}

func texRoot(t winenv.TeX) string {
	if t.Distro == "texlive" {
		return `C:\texlive`
	}
	// .../MiKTeX/miktex/bin/x64 -> .../MiKTeX
	return filepath.Dir(filepath.Dir(filepath.Dir(t.BinDir)))
}

// smokeTest is the only check that proves the whole chain works: LyX, the TeX
// distribution, babel-hebrew and culmus all have to cooperate to turn a Hebrew
// document into a PDF.
func smokeTest() *step.Step {
	return &step.Step{
		ID:       "smoketest",
		Name:     "Hebrew test compile",
		Needs:    []string{"settings"},
		Optional: true,
		Check: func(c *step.Context) (step.State, error) {
			// Always worth running: it verifies rather than configures.
			return step.Pending, nil
		},
		Apply: func(c *step.Context) error {
			l, ok := step.Get[winenv.LyX](c, keyLyX)
			if !ok {
				return fmt.Errorf("no LyX recorded")
			}
			// Deliberately an ASCII-only working directory: a Hebrew path here
			// would be testing the wrong thing.
			work, err := os.MkdirTemp("", "madlyx-smoke")
			if err != nil {
				return err
			}
			defer os.RemoveAll(work)

			src := filepath.Join(work, "smoketest.lyx")
			if err := payload.WriteTo("data/smoketest/smoketest.lyx", src); err != nil {
				return err
			}
			out := filepath.Join(work, "smoketest.pdf")

			c.UI.Detail("compiling (the first run builds a font cache and can take minutes)")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()

			cmd := exec.CommandContext(ctx, l.Exe, "-E", "pdf2", out, src)
			combined, runErr := cmd.CombinedOutput()
			c.Log.Logf("smoke test output: %s", string(combined))

			if st, err := os.Stat(out); err == nil && st.Size() > 0 {
				c.UI.Detail(fmt.Sprintf("produced a %d KB Hebrew PDF", st.Size()/1024))
				return nil
			}
			return fmt.Errorf("%s", diagnoseCompile(string(combined), runErr))
		},
	}
}

// diagnoseCompile maps the failures the guide documents onto their fixes, so a
// failed compile says what to do rather than just that it failed.
func diagnoseCompile(output string, runErr error) string {
	switch {
	case contains(output, "culmus.sty"):
		return "the culmus package is missing (guide p.21) - re-run and accept the Culmus step"
	case contains(output, "cp1255.def"):
		return "babel-hebrew is missing (guide p.25) - install it in MiKTeX Console"
	case contains(output, "not found"):
		return "a LaTeX package is missing; see the log for which"
	case contains(output, "Font") && contains(output, "not found"):
		return "a Hebrew font is not registered with TeX (guide pp.23-24) - re-run to re-register the Culmus maps"
	default:
		if runErr != nil {
			return fmt.Sprintf("the test document did not compile: %v", runErr)
		}
		return "the test document did not compile; see the log"
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && stringsContains(haystack, needle)
}
