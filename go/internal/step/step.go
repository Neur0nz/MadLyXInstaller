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
	"sync"
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
//
// Each step runs with its own Context whose UI is scoped to that step; they all
// point at the same discoveries. Sharing through a pointer rather than by value
// is what lets the per-step copies exist without duplicating the state or the
// lock that guards it.
type Context struct {
	UI      Reporter
	Log     Logger
	DryRun  bool
	Verbose bool

	initMu sync.Mutex
	shared *sharedState
}

// sharedState holds the discoveries steps pass to one another, e.g. where LyX
// turned out to be. Guarded because independent steps run concurrently.
type sharedState struct {
	mu sync.RWMutex
	m  map[string]any
}

// state returns the shared map, creating it on first use. Only ever called on
// the root Context - the per-step copies are handed the pointer directly - but
// concurrent steps reach it at the same time, hence the lock.
func (c *Context) state() *sharedState {
	c.initMu.Lock()
	defer c.initMu.Unlock()
	if c.shared == nil {
		c.shared = &sharedState{m: map[string]any{}}
	}
	return c.shared
}

// forStep derives the Context a single step runs with: the same discoveries,
// but a UI that attributes Detail and Progress to this step by name.
//
// Without this, concurrent steps overwrote one another's status text - during a
// real run "installing LyX" and "mathtools [7/35]" fought over one line, so the
// display contradicted itself every few hundred milliseconds.
func (c *Context) forStep(name string) *Context {
	return &Context{
		UI:      stepView{Reporter: c.UI, name: name},
		Log:     c.Log,
		DryRun:  c.DryRun,
		Verbose: c.Verbose,
		shared:  c.state(),
	}
}

// Get retrieves a value another step recorded, with the type asserted.
func Get[T any](c *Context, key string) (T, bool) {
	s := c.state()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var zero T
	v, ok := s.m[key]
	if !ok {
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// Set records a discovery for later steps.
func (c *Context) Set(key string, value any) {
	s := c.state()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
}

// Reporter is the subset of the ui package that steps are allowed to use.
// Keeping it an interface means steps can be tested without a terminal.
type Reporter interface {
	Section(title string)
	// Begin and End bracket a step. Several may be open at once, so both take
	// the step's name rather than relying on there being only one.
	Begin(step string)
	End(step string, ok bool, msg string)
	// Detail and Progress report what a step is doing. Steps call the unnamed
	// forms through the scoped Context they are given; the *For variants carry
	// the attribution the display needs when several steps are running.
	Detail(text string)
	Progress(text string, current, total int)
	DetailFor(step, text string)
	ProgressFor(step, text string, current, total int)
	// Overall reports how much of the plan has settled, so the display can say
	// "3 of 13" rather than only naming what happens to be running.
	Overall(done, total int)
	// Drop removes a step from the display without announcing an outcome.
	Drop(step string)
	Skipped(format string, a ...any)
	Warn(format string, a ...any)
	Info(format string, a ...any)
	Confirm(question, detail string, def bool) bool
	CanPrompt() bool
}

// stepView is a Reporter that knows which step is talking. Embedding the
// Reporter means only the two methods that need attribution are overridden.
type stepView struct {
	Reporter
	name string
}

func (v stepView) Detail(text string) { v.Reporter.DetailFor(v.name, text) }

func (v stepView) Progress(text string, current, total int) {
	v.Reporter.ProgressFor(v.name, text, current, total)
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

	// Interactive marks a step that may ask the user something. Such steps run
	// alone, never alongside others - two concurrent prompts would queue behind
	// each other and the user could not tell which question they were answering.
	Interactive bool
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

// Waves groups the plan into sets of steps that can run at the same time.
//
// A step joins the earliest wave after all of its Needs. The grouping is
// derived from the dependency graph the steps already declare, so nothing has
// to be marked parallel by hand - and a step that genuinely depends on another
// can never be scheduled alongside it.
//
// On a real install this matters: LyX takes around ten minutes while the TeX
// packages take seconds, and both only need the TeX distribution, so the
// shorter work costs nothing.
func (p *Plan) Waves() [][]*Step {
	depth := map[string]int{}
	var waves [][]*Step

	for _, s := range p.Steps {
		d := 0
		for _, need := range s.Needs {
			if nd, ok := depth[need]; ok && nd+1 > d {
				d = nd + 1
			}
		}
		depth[s.ID] = d
		for len(waves) <= d {
			waves = append(waves, nil)
		}
		waves[d] = append(waves[d], s)
	}
	return waves
}

// Run executes the plan.
//
// A step whose Check reports Satisfied is skipped, which is what makes both
// re-running and resuming after a failure work without any special casing.
// In a dry run nothing is applied and every Check still runs, so --dry-run
// reports exactly what would change.
//
// Independent steps run concurrently; see Waves. Results come back in the
// order the steps were declared, not the order they finished, so the summary
// reads the same however the scheduling worked out.
func (p *Plan) Run(ctx *Context) ([]Result, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	byID := map[string]*Result{}
	succeeded := map[string]bool{}

	settled := 0
	total := len(p.Steps)
	ctx.UI.Overall(settled, total)

	for _, wave := range p.Waves() {
		// Steps whose prerequisites failed are settled without running.
		var runnable []*Step
		for _, s := range wave {
			if missing := unmetNeeds(s, succeeded); missing != "" {
				byID[s.ID] = &Result{
					Step:    s,
					Skipped: true,
					Err:     fmt.Errorf("%w: %s", ErrSkippedDependency, missing),
				}
				ctx.Log.Logf("step %s skipped: needs %s", s.ID, missing)
				ctx.UI.Skipped("%s - skipped, %s did not succeed", s.Name, missing)
				settled++
				ctx.UI.Overall(settled, total)
				continue
			}
			runnable = append(runnable, s)
		}
		if len(runnable) == 0 {
			continue
		}

		// Steps that may prompt run on their own: two concurrent questions
		// would queue behind one another with no way to tell them apart.
		var concurrent, alone []*Step
		for _, s := range runnable {
			if s.Interactive {
				alone = append(alone, s)
				continue
			}
			concurrent = append(concurrent, s)
		}

		var out []*Result
		if len(concurrent) > 0 {
			if len(concurrent) > 1 && !ctx.DryRun {
				names := make([]string, 0, len(concurrent))
				for _, s := range concurrent {
					names = append(names, s.Name)
				}
				ctx.Log.Logf("running %d steps concurrently: %v", len(concurrent), names)
			}
			var wg sync.WaitGroup
			batch := make([]*Result, len(concurrent))
			for i, s := range concurrent {
				i, s := i, s
				wg.Add(1)
				go func() {
					defer wg.Done()
					batch[i] = runOne(ctx, s)
				}()
			}
			wg.Wait()
			out = append(out, batch...)
		}
		for _, s := range alone {
			out = append(out, runOne(ctx, s))
		}

		for _, r := range out {
			byID[r.Step.ID] = r
			if r.Err == nil {
				succeeded[r.Step.ID] = true
			}
			settled++
		}
		ctx.UI.Overall(settled, total)
	}

	// Report in declaration order regardless of completion order.
	results := make([]Result, 0, len(p.Steps))
	var firstErr error
	for _, s := range p.Steps {
		r, ok := byID[s.ID]
		if !ok {
			continue
		}
		results = append(results, *r)
		if r.Err != nil && !s.Optional && !errors.Is(r.Err, ErrSkippedDependency) && firstErr == nil {
			firstErr = fmt.Errorf("step %q: %w", s.ID, r.Err)
		}
	}
	return results, firstErr
}

// runOne executes a single step and reports on it. Safe to call concurrently;
// the UI serialises its own writes.
//
// The step sees a Context scoped to itself, so anything it reports is labelled
// with its name even when other steps are reporting at the same time.
func runOne(root *Context, s *Step) *Result {
	ctx := root.forStep(s.Name)
	res := &Result{Step: s}
	start := time.Now()

	state, err := s.Check(ctx)
	res.Before = state
	if err != nil {
		ctx.Log.Logf("step %s check failed: %v", s.ID, err)
		state = Unknown
	}

	switch {
	case state == Satisfied:
		res.Skipped = true
		res.Duration = time.Since(start)
		ctx.Log.Logf("step %s already satisfied", s.ID)
		ctx.UI.Skipped("%s - already done", s.Name)
		return res

	case ctx.DryRun:
		res.Duration = time.Since(start)
		ctx.UI.Info("would run: %s", s.Name)
		return res

	case s.Apply == nil:
		res.Skipped = true
		res.Duration = time.Since(start)
		ctx.UI.Skipped("%s - nothing to do", s.Name)
		return res
	}

	ctx.UI.Begin(s.Name)
	ctx.Log.Logf("step %s applying", s.ID)

	err = s.Apply(ctx)
	res.Duration = time.Since(start)
	res.Applied = err == nil
	res.Err = err

	switch {
	case err == nil:
		ctx.Log.Logf("step %s applied in %s", s.ID, res.Duration)
		ctx.UI.End(s.Name, true, s.Name)
	case s.Optional:
		ctx.Log.Logf("step %s failed (optional): %v", s.ID, err)
		ctx.UI.Drop(s.Name) // not a success; the warning below is the outcome
		ctx.UI.Warn("%s did not complete: %v", s.Name, err)
	default:
		ctx.Log.Logf("step %s failed: %v", s.ID, err)
		ctx.UI.End(s.Name, false, fmt.Sprintf("%s failed: %v", s.Name, err))
	}
	return res
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
		ctx.UI.Begin("Undoing " + r.Step.Name)
		if err := r.Step.Undo(ctx); err != nil {
			problems = append(problems, fmt.Errorf("undo %s: %w", r.Step.ID, err))
			ctx.UI.End("Undoing "+r.Step.Name, false, fmt.Sprintf("could not undo %s: %v", r.Step.Name, err))
			continue
		}
		ctx.UI.End("Undoing "+r.Step.Name, true, fmt.Sprintf("undid %s", r.Step.Name))
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
