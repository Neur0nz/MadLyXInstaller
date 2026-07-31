// Package step turns the installer from a procedure into data.
//
// The PowerShell version was a 173-line linear script with numbered comment
// blocks. That shape made four things impossible to add without rewriting it:
// a dry run, resuming after a failure, rolling back what had already been
// applied, and a doctor that agrees with the installer about what "configured"
// means. All four fall out of describing each unit of work as a value with a
// Check and an Apply.
//
// The cost of not having this was concrete: a run that installed LyX and then
// aborted left a half-configured machine with no way forward but to start over.
package step

import (
	"errors"
	"fmt"
	"time"
)

// State is the result of asking a step whether its work is already done.
type State int

const (
	// Pending means the step still needs to run.
	Pending State = iota
	// Satisfied means the step's desired outcome already holds. Re-running an
	// installer should skip these rather than redo them.
	Satisfied
	// Unknown means the check could not determine the answer, usually because
	// a prerequisite has not run yet. Treated as Pending, reported honestly.
	Unknown
)

func (s State) String() string {
	switch s {
	case Satisfied:
		return "satisfied"
	case Unknown:
		return "unknown"
	default:
		return "pending"
	}
}

// Context carries everything a step needs, so steps never reach for globals.
// The PowerShell version had six ambient $script: variables, one of which threw
// under StrictMode when a module was loaded on its own.
type Context struct {
	UI      Reporter
	Log     Logger
	DryRun  bool
	State   map[string]any // discoveries shared between steps, e.g. the LyX install
	Verbose bool
}

// Get retrieves a value another step recorded, with the type asserted.
func Get[T any](c *Context, key string) (T, bool) {
	var zero T
	v, ok := c.State[key]
	if !ok {
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// Set records a discovery for later steps.
func (c *Context) Set(key string, value any) {
	if c.State == nil {
		c.State = map[string]any{}
	}
	c.State[key] = value
}

// Reporter is the subset of the ui package that steps are allowed to use.
// Keeping it an interface means steps can be tested without a terminal.
type Reporter interface {
	Section(title string)
	Step(text string)
	Progress(text string, current, total int)
	Detail(text string)
	Done(format string, a ...any)
	Skipped(format string, a ...any)
	Warn(format string, a ...any)
	Fail(format string, a ...any)
	Info(format string, a ...any)
	Confirm(question, detail string, def bool) bool
	CanPrompt() bool
}

// Logger writes the durable record. Separate from Reporter so that what the
// user sees and what gets logged can differ.
type Logger interface {
	Logf(format string, a ...any)
}

// Step is one unit of installation work.
type Step struct {
	// ID is stable and machine-readable; used for --only, --skip and resume.
	ID string
	// Name is what the user sees.
	Name string
	// Needs lists step IDs that must have succeeded first.
	Needs []string

	// Check reports whether the work is already done. It must not change
	// anything: the doctor and the dry run both call it, and only it.
	Check func(*Context) (State, error)

	// Apply performs the work. Only called when Check returns Pending, and
	// never during a dry run.
	Apply func(*Context) error

	// Undo reverses Apply. Optional; steps without it are reported as
	// irreversible when a rollback is requested.
	Undo func(*Context) error

	// Optional steps do not fail the run when they fail.
	Optional bool
}

// Result records what happened to one step.
type Result struct {
	Step     *Step
	Before   State
	Applied  bool
	Skipped  bool
	Err      error
	Duration time.Duration
}

// Plan is an ordered set of steps.
type Plan struct {
	Steps []*Step
}

// ErrSkippedDependency marks a step that could not run because something it
// needed did not succeed. Reported distinctly so the summary does not present
// a cascade of consequences as a cascade of independent failures.
var ErrSkippedDependency = errors.New("a required earlier step did not succeed")

// Validate catches wiring mistakes - duplicate IDs, unknown or out-of-order
// dependencies - before anything touches the machine.
func (p *Plan) Validate() error {
	seen := map[string]bool{}
	for _, s := range p.Steps {
		if s.ID == "" {
			return fmt.Errorf("step %q has no ID", s.Name)
		}
		if seen[s.ID] {
			return fmt.Errorf("duplicate step ID %q", s.ID)
		}
		if s.Check == nil {
			return fmt.Errorf("step %q has no Check", s.ID)
		}
		for _, need := range s.Needs {
			if !seen[need] {
				return fmt.Errorf("step %q needs %q, which is not defined before it", s.ID, need)
			}
		}
		seen[s.ID] = true
	}
	return nil
}

// Run executes the plan.
//
// A step whose Check reports Satisfied is skipped, which is what makes both
// re-running and resuming after a failure work without any special casing.
// In a dry run nothing is applied and every Check still runs, so --dry-run
// reports exactly what would change.
func (p *Plan) Run(ctx *Context) ([]Result, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(p.Steps))
	succeeded := map[string]bool{}
	var firstErr error

	for _, s := range p.Steps {
		res := Result{Step: s}
		start := time.Now()

		if missing := unmetNeeds(s, succeeded); missing != "" {
			res.Skipped = true
			res.Err = fmt.Errorf("%w: %s", ErrSkippedDependency, missing)
			res.Duration = time.Since(start)
			results = append(results, res)
			ctx.Log.Logf("step %s skipped: needs %s", s.ID, missing)
			ctx.UI.Skipped("%s - skipped, %s did not succeed", s.Name, missing)
			continue
		}

		state, err := s.Check(ctx)
		res.Before = state
		if err != nil {
			ctx.Log.Logf("step %s check failed: %v", s.ID, err)
			state = Unknown
		}

		if state == Satisfied {
			res.Skipped = true
			res.Duration = time.Since(start)
			results = append(results, res)
			succeeded[s.ID] = true
			ctx.Log.Logf("step %s already satisfied", s.ID)
			ctx.UI.Skipped("%s - already done", s.Name)
			continue
		}

		if ctx.DryRun {
			res.Duration = time.Since(start)
			results = append(results, res)
			succeeded[s.ID] = true
			ctx.UI.Info("would run: %s", s.Name)
			continue
		}

		ctx.UI.Step(s.Name)
		ctx.Log.Logf("step %s applying", s.ID)

		if s.Apply == nil {
			res.Skipped = true
			res.Duration = time.Since(start)
			results = append(results, res)
			succeeded[s.ID] = true
			ctx.UI.Skipped("%s - nothing to do", s.Name)
			continue
		}

		err = s.Apply(ctx)
		res.Duration = time.Since(start)
		res.Applied = err == nil
		res.Err = err

		switch {
		case err == nil:
			succeeded[s.ID] = true
			ctx.Log.Logf("step %s applied in %s", s.ID, res.Duration)
		case s.Optional:
			ctx.Log.Logf("step %s failed (optional): %v", s.ID, err)
			ctx.UI.Warn("%s did not complete: %v", s.Name, err)
		default:
			ctx.Log.Logf("step %s failed: %v", s.ID, err)
			ctx.UI.Fail("%s failed: %v", s.Name, err)
			if firstErr == nil {
				firstErr = fmt.Errorf("step %q: %w", s.ID, err)
			}
		}
		results = append(results, res)
	}

	return results, firstErr
}

// Rollback undoes applied steps in reverse order. Steps without an Undo are
// reported rather than silently ignored, so the user knows what was left behind.
func (p *Plan) Rollback(ctx *Context, results []Result) []error {
	var problems []error
	for i := len(results) - 1; i >= 0; i-- {
		r := results[i]
		if !r.Applied {
			continue
		}
		if r.Step.Undo == nil {
			problems = append(problems, fmt.Errorf("%s cannot be undone automatically", r.Step.Name))
			ctx.UI.Warn("%s cannot be undone automatically", r.Step.Name)
			continue
		}
		ctx.UI.Step("Undoing " + r.Step.Name)
		if err := r.Step.Undo(ctx); err != nil {
			problems = append(problems, fmt.Errorf("undo %s: %w", r.Step.ID, err))
			ctx.UI.Fail("could not undo %s: %v", r.Step.Name, err)
			continue
		}
		ctx.UI.Done("undid %s", r.Step.Name)
	}
	return problems
}

// Diagnose runs every Check and nothing else. The doctor is this function,
// which is what stops the installer and the doctor from drifting apart about
// what "configured" means - in the PowerShell version that knowledge was
// written out twice.
func (p *Plan) Diagnose(ctx *Context) []Result {
	results := make([]Result, 0, len(p.Steps))
	for _, s := range p.Steps {
		start := time.Now()
		state, err := s.Check(ctx)
		results = append(results, Result{
			Step:     s,
			Before:   state,
			Err:      err,
			Duration: time.Since(start),
		})
	}
	return results
}

func unmetNeeds(s *Step, succeeded map[string]bool) string {
	for _, need := range s.Needs {
		if !succeeded[need] {
			return need
		}
	}
	return ""
}
