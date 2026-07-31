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

// defenderExclusions speeds up compiling. LaTeX creates and deletes many small
// files per run, and MiKTeX writes thousands when installing packages.
func defenderExclusions() *step.Step {
	return &step.Step{
		ID:          "defender",
		Name:        "Defender exclusions",
		Needs:       []string{"tex"},
		Optional:    true,
		Interactive: true, // asks before changing Defender configuration
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
			// The rest of the install deliberately runs without administrator
			// rights, so this is the one step that can be out of reach. Say how
			// to get it rather than just reporting that it failed.
			if !winsys.IsAdmin() {
				return fmt.Errorf("needs administrator rights - re-run with --elevate if you want it")
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

// reconfigure makes LyX rescan TeX after the packages are in place.
//
// It has to come after both the LyX install and the package install, which the
// Needs below express - and because those two run concurrently, this is the
// step that joins them back together.
func reconfigure() *step.Step {
	return &step.Step{
		ID:       "reconfigure",
		Name:     "Rescanning TeX from LyX",
		Needs:    []string{"userdir", "texpackages"},
		Optional: true,
		Check: func(c *step.Context) (step.State, error) {
			dir, ok := step.Get[string](c, keyUserDir)
			if !ok {
				return step.Unknown, nil
			}
			// heb-article is the class the MadLyX templates use, so it is the
			// one whose availability actually matters.
			if textclassAvailable(dir, "heb-article") {
				return step.Satisfied, nil
			}
			return step.Pending, nil
		},
		Apply: func(c *step.Context) error {
			l, ok := step.Get[winenv.LyX](c, keyLyX)
			if !ok {
				return fmt.Errorf("no LyX recorded")
			}
			c.UI.Detail("this takes a couple of minutes")
			return reconfigureLyX(c, l)
		},
	}
}

// smokeTest proves the Hebrew toolchain works: the TeX distribution,
// babel-hebrew, culmus and the David font all have to cooperate to turn a
// Hebrew document into a PDF.
//
// It compiles with pdflatex directly rather than by asking LyX to export, and
// that is a deliberate retreat. Driving LyX was the better test - it exercised
// the same path the user's own Ctrl+R does - but it could not be made quiet.
// Measured on a real machine, running LyX's export returned zero bytes on
// stdout and stderr under every process creation flag Windows offers
// (CREATE_NO_WINDOW, DETACHED_PROCESS, CREATE_NEW_CONSOLE), while pdflatex's
// output landed in the user's console every single time. Nothing about how we
// spawn the process can change that, so pages of METAFONT output scrolled
// through the middle of the progress display on every fresh install.
//
// Compiling the exported .tex ourselves gives up verifying LyX's export step
// and gains three things: no console spam, output we can actually read, and a
// real LaTeX log to diagnose from. What LyX contributes is checked separately -
// that it is installed, configured, and reports heb-article as available.
func smokeTest() *step.Step {
	return &step.Step{
		ID:       "smoketest",
		Name:     "Hebrew test compile",
		Needs:    []string{"settings", "reconfigure"},
		Optional: true,
		Check: func(c *step.Context) (step.State, error) {
			// Always worth running: it verifies rather than configures.
			return step.Pending, nil
		},
		Apply: func(c *step.Context) error {
			t, ok := step.Get[winenv.TeX](c, keyTeX)
			if !ok {
				return fmt.Errorf("no TeX distribution recorded")
			}
			// Deliberately an ASCII-only working directory: a Hebrew path here
			// would be testing the wrong thing.
			work, err := os.MkdirTemp("", "madlyx-smoke")
			if err != nil {
				return err
			}
			defer os.RemoveAll(work)

			// Shipped byte-exact in cp1255, which is the encoding its own
			// \usepackage[cp1255]{inputenc} declares. Re-encoding it to UTF-8
			// would turn every Hebrew character into mojibake.
			src := filepath.Join(work, "smoketest.tex")
			if err := payload.WriteTo("data/smoketest/smoketest.tex", src); err != nil {
				return err
			}

			c.UI.Detail("compiling (the first run builds a font cache and can take minutes)")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()

			cmd := exec.CommandContext(ctx, filepath.Join(t.BinDir, "pdflatex.exe"),
				"-interaction=nonstopmode", "-halt-on-error", "smoketest.tex")
			cmd.Dir = work
			hideConsole(cmd)
			combined, runErr := cmd.CombinedOutput()
			c.Log.Logf("smoke test output: %s", string(combined))

			out := filepath.Join(work, "smoketest.pdf")
			if st, err := os.Stat(out); err == nil && st.Size() > 0 {
				c.UI.Detail(fmt.Sprintf("produced a %d KB Hebrew PDF", st.Size()/1024))
				return nil
			}

			// The .log records which package was missing even when the console
			// output is unhelpful, so prefer it and fall back to what we caught.
			texLog, _ := os.ReadFile(filepath.Join(work, "smoketest.log"))
			c.Log.Logf("latex log: %s", string(texLog))
			return fmt.Errorf("%s", diagnoseCompile(string(texLog)+string(combined), runErr))
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
