// Package steps defines what the installer actually does, as data.
//
// Each step names its own Check, so the installer, the dry run and the doctor
// all read the same definition of "done". Adding a step means adding an entry
// to Build, not editing a procedure.
package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Neur0nz/MadLyXInstaller/go/internal/lyxcfg"
	"github.com/Neur0nz/MadLyXInstaller/go/internal/payload"
	"github.com/Neur0nz/MadLyXInstaller/go/internal/pkgmgr"
	"github.com/Neur0nz/MadLyXInstaller/go/internal/step"
	"github.com/Neur0nz/MadLyXInstaller/go/internal/winenv"
)

// Options are the choices a run was started with.
type Options struct {
	TeXDistribution string // "auto", "miktex", "texlive"
	SkipSmokeTest   bool
	SkipSystemSteps bool
	Runner          pkgmgr.Runner
}

// Keys used to pass discoveries between steps.
const (
	keyLyX     = "lyx"
	keyTeX     = "tex"
	keyUserDir = "lyx.userdir"
)

// Build returns the ordered plan.
func Build(o Options) *step.Plan {
	p := &step.Plan{}
	p.Steps = append(p.Steps,
		preflight(),
		texDistribution(o),
		lyxInstall(o),
		lyxUserDir(),
		texPackages(o),
		culmus(o),
		settings(),
		shortcuts(),
		templates(),
		macros(),
		reconfigure(),
	)
	if !o.SkipSystemSteps {
		p.Steps = append(p.Steps, defenderExclusions())
	}
	if !o.SkipSmokeTest {
		p.Steps = append(p.Steps, smokeTest())
	}
	return p
}

// ---------------------------------------------------------------------------

func preflight() *step.Step {
	return &step.Step{
		ID:   "preflight",
		Name: "Checking your system",
		Check: func(c *step.Context) (step.State, error) {
			// Advisory only: there is nothing to install, so it is never
			// "pending" in a way Apply could fix.
			return step.Satisfied, nil
		},
		Apply: func(c *step.Context) error { return nil },
	}
}

func texDistribution(o Options) *step.Step {
	return &step.Step{
		ID:    "tex",
		Name:  "TeX distribution",
		Needs: []string{"preflight"},
		Check: func(c *step.Context) (step.State, error) {
			if t, ok := winenv.FindTeX(); ok {
				c.Set(keyTeX, t)
				return step.Satisfied, nil
			}
			return step.Pending, nil
		},
		Apply: func(c *step.Context) error {
			distro := chooseDistro(o.TeXDistribution)
			c.UI.Detail(fmt.Sprintf("installing %s (this takes several minutes)", distro))

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
			defer cancel()

			id := "MiKTeX.MiKTeX"
			if distro == "texlive" {
				// TeX Live has no winget package worth using; MiKTeX is the
				// default precisely because it can install packages on demand.
				return fmt.Errorf("TeX Live must be installed manually from https://tug.org/texlive/")
			}
			if err := pkgmgr.WingetInstallProgress(ctx, o.Runner, id, nil,
				func(phase string) { c.UI.Detail(phase) }); err != nil {
				c.Log.Logf("winget reported: %v", err)
			}

			// winget returns once it has handed off to the installer, which is
			// still writing. Poll rather than checking once.
			c.UI.Detail("waiting for the installation to settle")
			t, ok := winenv.WaitFor(3*time.Minute, 2*time.Second, winenv.FindMiKTeX)
			if !ok {
				return fmt.Errorf("%s did not appear after installing", distro)
			}
			c.Set(keyTeX, t)

			// The reason MiKTeX is preferred: missing packages install themselves.
			if err := pkgmgr.EnableMiKTeXAutoInstall(ctx, o.Runner, t.BinDir); err != nil {
				c.UI.Warn("could not enable automatic package installation: %v", err)
			}
			return nil
		},
	}
}

func chooseDistro(requested string) string {
	if requested == "texlive" {
		return "texlive"
	}
	if requested == "auto" {
		// Hebrew in the profile path is the guide's leading cause of failure,
		// and TeX Live copes with it better.
		if _, hebrew := winenv.ProfileHasHebrew(); hebrew {
			return "texlive"
		}
	}
	return "miktex"
}

func lyxInstall(o Options) *step.Step {
	return &step.Step{
		ID:    "lyx",
		Name:  "LyX",
		Needs: []string{"tex"},
		Check: func(c *step.Context) (step.State, error) {
			if l, ok := winenv.FindLyX(); ok {
				c.Set(keyLyX, l)
				return step.Satisfied, nil
			}
			return step.Pending, nil
		},
		Apply: func(c *step.Context) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			progress := func(phase string) { c.UI.Detail(phase) }
			c.UI.Detail("installing LyX")

			// Install for this user only, which needs no administrator rights.
			//
			// LyX's installer is built with MULTIUSER_INSTALLMODE_COMMANDLINE
			// defined, so it accepts /CurrentUser; in that mode it writes to
			// %LOCALAPPDATA%\Programs and HKCU and nothing else. Measured on a
			// clean machine from an unelevated shell: 41 seconds, exit 0, no UAC
			// dialog. MiKTeX already installs per-user, so this is what makes a
			// complete setup possible without a single elevation prompt - which
			// matters because the previous machine-wide install produced a UAC
			// dialog that could open behind another window, where it once
			// blocked the run for the full 30-minute timeout.
			//
			// A watchdog still covers the case where something does put a dialog
			// in front of the user.
			done := make(chan struct{})
			defer close(done)
			go func() {
				select {
				case <-time.After(2 * time.Minute):
					c.UI.Detail("still working - if Windows asked for permission, " +
						"the prompt may be waiting behind this window")
				case <-done:
				}
			}()

			if err := pkgmgr.WingetInstallProgress(ctx, o.Runner, "LyX.LyX",
				[]string{"/CurrentUser"}, progress); err != nil {
				c.Log.Logf("winget (per-user) reported: %v", err)
			}

			c.UI.Detail("waiting for the installation to settle")
			if l, ok := winenv.WaitFor(3*time.Minute, 2*time.Second, winenv.FindLyX); ok {
				c.Set(keyLyX, l)
				return nil
			}

			// Fall back to whatever the installer does by default. This may ask
			// for permission, which is worse than not asking but far better than
			// failing outright on a machine where the per-user path did not work.
			c.Log.Logf("per-user install did not produce a LyX; retrying machine-wide")
			c.UI.Detail("retrying - Windows may ask for permission")
			if err := pkgmgr.WingetInstallProgress(ctx, o.Runner, "LyX.LyX", nil, progress); err != nil {
				c.Log.Logf("winget reported: %v", err)
			}
			if l, ok := winenv.WaitFor(3*time.Minute, 2*time.Second, winenv.FindLyX); ok {
				c.Set(keyLyX, l)
				return nil
			}

			if pkgmgr.Available("choco") {
				c.UI.Detail("trying Chocolatey")
				if err := pkgmgr.ChocoInstall(ctx, o.Runner, "lyx"); err != nil {
					c.Log.Logf("choco reported: %v", err)
				}
				if l, ok := winenv.WaitFor(3*time.Minute, 2*time.Second, winenv.FindLyX); ok {
					c.Set(keyLyX, l)
					return nil
				}
			}
			return fmt.Errorf("LyX did not appear after installing; install it from https://www.lyx.org/Download and re-run")
		},
	}
}

func lyxUserDir() *step.Step {
	return &step.Step{
		ID:    "userdir",
		Name:  "LyX settings folder",
		Needs: []string{"lyx"},
		Check: func(c *step.Context) (step.State, error) {
			l, ok := step.Get[winenv.LyX](c, keyLyX)
			if !ok {
				return step.Unknown, nil
			}
			if dir, ok := winenv.FindLyXUserDir(l); ok {
				c.Set(keyUserDir, dir)
				return step.Satisfied, nil
			}
			return step.Pending, nil
		},
		Apply: func(c *step.Context) error {
			l, _ := step.Get[winenv.LyX](c, keyLyX)
			c.UI.Detail("starting LyX once so it creates its settings folder")

			// Poll for the folder rather than sleeping a fixed time and killing
			// the process, which races on a cold start and risks killing LyX
			// mid-write.
			dir, err := startLyXOnce(c, l)
			if err != nil {
				return err
			}
			c.Set(keyUserDir, dir)
			return nil
		},
	}
}

func texPackages(o Options) *step.Step {
	return &step.Step{
		ID:    "texpackages",
		Name:  "Hebrew LaTeX packages",
		Needs: []string{"tex"},
		Check: func(c *step.Context) (step.State, error) {
			t, ok := step.Get[winenv.TeX](c, keyTeX)
			if !ok {
				return step.Unknown, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			// culmus and babel-hebrew are the two that actually matter; if TeX
			// can resolve both, the rest is noise.
			if pkgmgr.KpsewhichFinds(ctx, o.Runner, t.BinDir, "culmus.sty") &&
				pkgmgr.KpsewhichFinds(ctx, o.Runner, t.BinDir, "cp1255.def") {
				return step.Satisfied, nil
			}
			return step.Pending, nil
		},
		Apply: func(c *step.Context) error {
			t, _ := step.Get[winenv.TeX](c, keyTeX)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
			defer cancel()

			err := pkgmgr.InstallTeXPackages(ctx, o.Runner, t.Distro, t.BinDir,
				func(name string, i, total int) { c.UI.Progress(name, i, total) })
			if err != nil {
				// On MiKTeX anything missing installs itself on first use.
				c.UI.Warn("%v", err)
			}
			return nil
		},
	}
}

func culmus(o Options) *step.Step {
	return &step.Step{
		ID:          "culmus",
		Name:        "Culmus Hebrew fonts",
		Needs:       []string{"texpackages"},
		Optional:    true,
		Interactive: true, // asks before downloading the HUJI installer
		Check: func(c *step.Context) (step.State, error) {
			t, ok := step.Get[winenv.TeX](c, keyTeX)
			if !ok {
				return step.Unknown, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if pkgmgr.KpsewhichFinds(ctx, o.Runner, t.BinDir, "culmus.sty") {
				return step.Satisfied, nil
			}
			return step.Pending, nil
		},
		Apply: func(c *step.Context) error {
			t, _ := step.Get[winenv.TeX](c, keyTeX)
			if t.Distro != "miktex" {
				return nil // TeX Live ships culmus in its repository
			}
			// MiKTeX has no culmus package, which is the 'culmus.sty not found'
			// failure on p.21 of the guide.
			ok := c.UI.Confirm(
				"Download and run the Culmus-for-MiKTeX installer?",
				"MiKTeX does not ship the culmus package, which causes\n"+
					"'LaTeX Error: File culmus.sty not found' on Hebrew documents.\n"+
					"Source: http://www.ma.huji.ac.il/~sameti/tex/culmusmiktex0.2.2.exe\n"+
					"(the installer linked by the MadLyX guide, hosted by HUJI)",
				true)
			if !ok {
				return fmt.Errorf("skipped; Hebrew export may fail with culmus.sty errors")
			}
			return installCulmus(c, t)
		},
	}
}

func settings() *step.Step {
	return &step.Step{
		ID:    "settings",
		Name:  "MadLyX settings",
		Needs: []string{"userdir", "tex"},
		Check: func(c *step.Context) (step.State, error) {
			dir, ok := step.Get[string](c, keyUserDir)
			if !ok {
				return step.Unknown, nil
			}
			s, err := buildSettings(c)
			if err != nil {
				return step.Unknown, err
			}
			applied, err := lyxcfg.IsApplied(filepath.Join(dir, "preferences"), s)
			if err != nil {
				return step.Unknown, err
			}
			if applied {
				return step.Satisfied, nil
			}
			return step.Pending, nil
		},
		Apply: func(c *step.Context) error {
			dir, _ := step.Get[string](c, keyUserDir)
			s, err := buildSettings(c)
			if err != nil {
				return err
			}
			backup, err := lyxcfg.Apply(filepath.Join(dir, "preferences"), s)
			if err != nil {
				return err
			}
			if backup != "" {
				c.UI.Detail("backed up your previous settings to " + filepath.Base(backup))
			}
			c.UI.Detail(fmt.Sprintf("wrote %d settings", s.Len()))
			return nil
		},
		Undo: func(c *step.Context) error {
			dir, ok := step.Get[string](c, keyUserDir)
			if !ok {
				return nil
			}
			pref := filepath.Join(dir, "preferences")
			backup, found := lyxcfg.LatestBackup(pref)
			if !found {
				return fmt.Errorf("no backup to restore")
			}
			b, err := os.ReadFile(backup)
			if err != nil {
				return err
			}
			return os.WriteFile(pref, b, 0o644)
		},
	}
}

// buildSettings assembles the preference set for the detected environment.
//
// Every key was verified against LyX's LyXRC.cpp tag table.
func buildSettings(c *step.Context) (*lyxcfg.Settings, error) {
	t, ok := step.Get[winenv.TeX](c, keyTeX)
	if !ok {
		return nil, fmt.Errorf("no TeX distribution recorded")
	}
	l, _ := step.Get[winenv.LyX](c, keyLyX)
	dir, _ := step.Get[string](c, keyUserDir)

	s := lyxcfg.NewSettings()
	// Guide p.14: every guide and forum answer online is in English, and menu
	// keyboard navigation depends on English accelerators.
	s.Set("gui_language", "english")
	// Figure 0.4: LyX supplies the Hebrew, Windows stays on English.
	s.Set("kbmap", "true")
	s.SetQuoted("kbmap_primary", "null")
	s.SetQuoted("kbmap_secondary", "hebrew")
	// Figure 0.3.
	s.Set("visual_cursor", "true")
	// Figure 0.5.
	s.Set("scroll_below_document", "true")
	// Guide pp.75-77: the MadLyX bind file has no \bind_file line, so without
	// this the stock shortcuts (Ctrl+S, Ctrl+M) can stop working.
	s.SetQuoted("bind_file", "cua")
	s.SetQuoted("path_prefix", t.BinDir)
	// Not in the guide: renders maths and images inline instead of source boxes.
	s.Set("preview", "on")
	s.Set("autosave", "300")
	s.Set("make_backup", "true")
	if dir != "" {
		s.SetQuoted("backupdir_path", filepath.Join(dir, "backups"))
		s.SetQuoted("template_path", filepath.Join(dir, "templates"))
	}
	// Guide p.46: LyX 2.4's in-editor colours are too loud to read comfortably.
	if l.WantsMutedColors() {
		for name, hex := range map[string]string{
			"green": "#b5bd68", "red": "#cc6666", "blue": "#81a2be",
			"magenta": "#b294bb", "cyan": "#8abeb7", "yellow": "#f0c674",
		} {
			s.SetColor(name, hex)
		}
	}
	return s, nil
}

func shortcuts() *step.Step {
	return &step.Step{
		ID:    "shortcuts",
		Name:  "MadLyX shortcuts",
		Needs: []string{"userdir"},
		Check: func(c *step.Context) (step.State, error) {
			dir, ok := step.Get[string](c, keyUserDir)
			if !ok {
				return step.Unknown, nil
			}
			l, _ := step.Get[winenv.LyX](c, keyLyX)
			series, _ := l.BindSeries()
			want, err := payload.Read(payload.Bind(series))
			if err != nil {
				return step.Unknown, err
			}
			got, err := os.ReadFile(filepath.Join(dir, "bind", "user.bind"))
			if err != nil {
				return step.Pending, nil
			}
			if len(got) == len(want) {
				return step.Satisfied, nil
			}
			return step.Pending, nil
		},
		Apply: func(c *step.Context) error {
			dir, _ := step.Get[string](c, keyUserDir)
			l, _ := step.Get[winenv.LyX](c, keyLyX)
			series, exact := l.BindSeries()
			if !exact {
				c.UI.Detail(fmt.Sprintf("LyX %s has no dedicated build; using the %s file, which LyX converts on load",
					l.Version, series))
			}
			target := filepath.Join(dir, "bind", "user.bind")
			if _, err := lyxcfg.BackupFile(target); err != nil {
				return err
			}
			return payload.WriteTo(payload.Bind(series), target)
		},
	}
}

func templates() *step.Step {
	return &step.Step{
		ID:    "templates",
		Name:  "Document templates",
		Needs: []string{"userdir"},
		Check: func(c *step.Context) (step.State, error) {
			dir, ok := step.Get[string](c, keyUserDir)
			if !ok {
				return step.Unknown, nil
			}
			entries, err := os.ReadDir(filepath.Join(dir, "templates"))
			if err != nil {
				return step.Pending, nil
			}
			var n int
			for _, e := range entries {
				if filepath.Ext(e.Name()) == ".lyx" {
					n++
				}
			}
			if n >= 6 { // five templates plus defaults.lyx
				return step.Satisfied, nil
			}
			return step.Pending, nil
		},
		Apply: func(c *step.Context) error {
			dir, _ := step.Get[string](c, keyUserDir)
			dest := filepath.Join(dir, "templates")
			n, err := payload.WriteTree("data/templates", dest)
			if err != nil {
				return err
			}
			// LyX uses templates/defaults.lyx for a plain File > New.
			if err := payload.WriteTo("data/templates/02-hebrew-article.lyx",
				filepath.Join(dest, "defaults.lyx")); err != nil {
				return err
			}
			c.UI.Detail(fmt.Sprintf("installed %d templates; press Ctrl+Shift+N in LyX to use them", n))
			return nil
		},
	}
}

func macros() *step.Step {
	return &step.Step{
		ID:    "macros",
		Name:  "Macros and shared preamble",
		Needs: []string{"lyx"},
		Check: func(c *step.Context) (step.State, error) {
			dest := madlyxDocsDir()
			if _, err := os.Stat(filepath.Join(dest, "macros", "madlyx-macros-he.lyx")); err == nil {
				return step.Satisfied, nil
			}
			return step.Pending, nil
		},
		Apply: func(c *step.Context) error {
			dest := madlyxDocsDir()
			if _, err := payload.WriteTree("data/macros", filepath.Join(dest, "macros")); err != nil {
				return err
			}
			if err := payload.WriteTo("data/preamble/madlyx-preamble.tex",
				filepath.Join(dest, "madlyx-preamble.tex")); err != nil {
				return err
			}
			c.UI.Detail("installed to " + dest)
			return nil
		},
	}
}

func madlyxDocsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "MadLyX")
	}
	return filepath.Join(home, "Documents", "MadLyX")
}
