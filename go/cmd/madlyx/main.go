// Command madlyx installs and configures LyX for writing Hebrew mathematics,
// following "The MadLyX" by Kali.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Neur0nz/MadLyXInstaller/go/internal/pkgmgr"
	"github.com/Neur0nz/MadLyXInstaller/go/internal/step"
	"github.com/Neur0nz/MadLyXInstaller/go/internal/steps"
	"github.com/Neur0nz/MadLyXInstaller/go/internal/ui"
	"github.com/Neur0nz/MadLyXInstaller/go/internal/winenv"
)

// Injected at release time with -ldflags.
var (
	version = "dev"
	commit  = "unknown"
)

type globalFlags struct {
	dryRun      bool
	yes         bool
	texDistro   string
	skipSmoke   bool
	skipSystem  bool
	noElevate   bool
	verbose     bool
}

func main() {
	var g globalFlags

	root := &cobra.Command{
		Use:   "madlyx",
		Short: "Set up LyX for Hebrew mathematics on Windows",
		Long: "Installs a TeX distribution and LyX, applies the full Hebrew setup from\n" +
			"The MadLyX by Kali, and verifies it by compiling a Hebrew document.\n\n" +
			"Safe to re-run: work already done is skipped, and existing settings are\n" +
			"backed up before anything is changed.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       fmt.Sprintf("%s (%s)", version, commit),
		RunE:          func(cmd *cobra.Command, args []string) error { return runInstall(g) },
	}

	root.PersistentFlags().BoolVar(&g.dryRun, "dry-run", false, "report what would change without changing anything")
	root.PersistentFlags().BoolVarP(&g.yes, "yes", "y", false, "do not ask questions; system changes are skipped, not assumed")
	root.PersistentFlags().StringVar(&g.texDistro, "tex", "auto", "TeX distribution: auto, miktex or texlive")
	root.PersistentFlags().BoolVar(&g.skipSmoke, "skip-test", false, "skip the final Hebrew test compile")
	root.PersistentFlags().BoolVar(&g.skipSystem, "skip-system", false, "do not offer changes outside LyX")
	root.PersistentFlags().BoolVar(&g.noElevate, "no-elevate", false, "never offer to restart with administrator rights")
	root.PersistentFlags().BoolVarP(&g.verbose, "verbose", "v", false, "log more detail")

	root.AddCommand(&cobra.Command{
		Use:           "install",
		Short:         "Install and configure everything (the default)",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, args []string) error { return runInstall(g) },
	})

	root.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Check the installation and report problems; changes nothing",
		Long: "Runs every step's Check and nothing else. Because it shares those checks\n" +
			"with the installer, the two cannot disagree about what 'configured' means.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, args []string) error { return runDoctor(g) },
	})

	root.AddCommand(&cobra.Command{
		Use:           "uninstall",
		Short:         "Undo the changes this installer made",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, args []string) error { return runUninstall(g) },
	})

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "\nmadlyx: %v\n", err)
		os.Exit(1)
	}
}

// newContext wires up the shared plumbing for a run.
func newContext(g globalFlags) (*step.Context, *logger, *ui.UI) {
	u := ui.New(g.yes)
	log := newLogger(g.verbose)
	ctx := &step.Context{
		UI:      u,
		Log:     log,
		DryRun:  g.dryRun,
		State:   map[string]any{},
		Verbose: g.verbose,
	}
	return ctx, log, u
}

func options(g globalFlags, log *logger) steps.Options {
	return steps.Options{
		TeXDistribution: g.texDistro,
		SkipSmokeTest:   g.skipSmoke,
		SkipSystemSteps: g.skipSystem,
		Runner:          pkgmgr.ExecRunner{Log: log.Logf},
	}
}

func runInstall(g globalFlags) error {
	ctx, log, u := newContext(g)
	defer log.Close()

	u.Title("MadLyX Installer", version)
	if g.dryRun {
		u.Info("dry run: nothing will be changed")
	}
	reportEnvironment(u)

	if err := maybeElevate(g, u); err != nil {
		return err
	}

	plan := steps.Build(options(g, log))
	results, runErr := plan.Run(ctx)

	summarise(u, results, g.dryRun)
	u.Info("log: %s", log.Path())

	if runErr != nil {
		u.Info("re-run to continue from where it stopped, or 'madlyx doctor' to check")
		return runErr
	}
	if !g.dryRun {
		printNextSteps(u)
	}
	return nil
}

func runDoctor(g globalFlags) error {
	ctx, log, u := newContext(g)
	defer log.Close()

	u.Title("MadLyX Doctor", version)
	reportEnvironment(u)

	plan := steps.Build(options(g, log))
	results := plan.Diagnose(ctx)

	rows := [][]string{{"Step", "State", "Note"}}
	var problems, offers int
	for _, r := range results {
		note := ""
		if r.Err != nil {
			note = r.Err.Error()
		}

		state := r.Before.String()
		switch {
		case r.Before == step.Satisfied:
			// nothing to report
		case r.Step.Optional:
			// Optional steps are offers the user may have declined, and the
			// test compile verifies rather than configures, so it is never
			// "satisfied". Neither is a fault in the installation.
			state = "not applied"
			if note == "" {
				note = "optional"
			}
			offers++
		default:
			problems++
		}
		rows = append(rows, []string{r.Step.Name, state, note})
	}
	u.Summary(rows)

	if problems == 0 {
		u.Done("installation looks healthy")
		if offers > 0 {
			u.Info("%d optional step(s) not applied - run 'madlyx install' if you want them", offers)
		}
		return nil
	}
	u.Warn("%d step(s) need attention - run 'madlyx install' to fix", problems)
	return fmt.Errorf("%d problem(s) found", problems)
}

func runUninstall(g globalFlags) error {
	ctx, log, u := newContext(g)
	defer log.Close()

	u.Title("MadLyX Uninstall", version)
	if !u.Confirm("Undo the changes MadLyX made?",
		"Restores your previous LyX settings from the backups taken at install time\n"+
			"and removes the Defender exclusions, if any were added.\n\n"+
			"LyX and the TeX distribution are left installed - remove those with winget\n"+
			"if you want them gone.", false) {
		u.Info("nothing was changed")
		return nil
	}

	plan := steps.Build(options(g, log))
	// Treat every step as applied so Rollback attempts each one that can be
	// undone, and reports the ones that cannot.
	results := make([]step.Result, 0, len(plan.Steps))
	for _, s := range plan.Steps {
		results = append(results, step.Result{Step: s, Applied: true})
	}
	problems := plan.Rollback(ctx, results)

	if len(problems) > 0 {
		u.Warn("%d item(s) could not be undone automatically", len(problems))
	}
	u.Done("done")
	return nil
}

func reportEnvironment(u *ui.UI) {
	if home, hebrew := winenv.ProfileHasHebrew(); hebrew {
		u.Warn("your user folder contains Hebrew: %s", home)
		u.Info("this is the leading cause of failed PDF export; keep documents somewhere like C:\\Studies")
	}
}

// maybeElevate offers a restart with administrator rights.
//
// Gated on being able to prompt: without a console there is nobody to accept
// the UAC dialog, so offering it would hang rather than help.
func maybeElevate(g globalFlags, u *ui.UI) error {
	if g.noElevate || g.dryRun || !u.CanPrompt() || isAdmin() {
		return nil
	}
	ok := u.Confirm("Restart with administrator rights?",
		"Not required. Without it these are skipped:\n"+
			"  - installing LyX for all users (a per-user install still works)\n"+
			"  - Defender exclusions, which speed up compiling\n\n"+
			"Everything else works without administrator rights.", true)
	if !ok {
		return nil
	}
	code, err := relaunchElevated(os.Args[1:])
	if err != nil {
		u.Warn("could not restart elevated: %v", err)
		return nil
	}
	os.Exit(code)
	return nil
}

func summarise(u *ui.UI, results []step.Result, dryRun bool) {
	rows := [][]string{{"Step", "Result", "Time"}}
	for _, r := range results {
		outcome := "done"
		switch {
		case dryRun && r.Before != step.Satisfied:
			outcome = "would run"
		case r.Err != nil && r.Step.Optional:
			outcome = "skipped"
		case r.Err != nil:
			outcome = "FAILED"
		case r.Skipped && r.Before == step.Satisfied:
			outcome = "already done"
		case r.Skipped:
			outcome = "skipped"
		}
		rows = append(rows, []string{r.Step.Name, outcome, r.Duration.Round(time.Millisecond).String()})
	}
	u.Summary(rows)
}

func printNextSteps(u *ui.UI) {
	u.Section("Things to know")
	for _, line := range []string{
		"F12 switches between Hebrew and English inside LyX.",
		"Keep Windows itself on ENG - LyX supplies the Hebrew.",
		"Ctrl+Shift+N starts a document from a MadLyX template.",
		"Check the shortcuts loaded: in a maths box, Alt+W then A gives a Greek alpha.",
		"Keep Hebrew out of folder names above your documents.",
	} {
		u.Info("%s", line)
	}
}

// logger writes the durable record next to the user's other app data.
type logger struct {
	f       *os.File
	path    string
	verbose bool
}

func newLogger(verbose bool) *logger {
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "MadLyXInstaller")
	if dir == "MadLyXInstaller" {
		dir = filepath.Join(os.TempDir(), "MadLyXInstaller")
	}
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, fmt.Sprintf("install-%s.log", time.Now().Format("20060102-150405")))
	f, err := os.Create(path)
	if err != nil {
		return &logger{verbose: verbose}
	}
	fmt.Fprintf(f, "=== madlyx %s (%s) at %s ===\n", version, commit, time.Now().Format(time.RFC3339))
	return &logger{f: f, path: path, verbose: verbose}
}

func (l *logger) Logf(format string, a ...any) {
	if l.f == nil {
		return
	}
	fmt.Fprintf(l.f, "[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
}

func (l *logger) Path() string { return l.path }

func (l *logger) Close() {
	if l.f != nil {
		l.f.Close()
	}
}
