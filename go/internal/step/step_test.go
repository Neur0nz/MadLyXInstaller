package step

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

// fakeUI records what a run reported, so tests can assert on behaviour without
// a terminal. This is only possible because ui and logic are separate now.
type fakeUI struct {
	mu      sync.Mutex
	skipped []string
	failed  []string
	steps   []string
	infos   []string
	answers map[string]bool
	prompts []string
	// details records who said what, which is what proves concurrent steps no
	// longer overwrite one another's status.
	details []string
	overall [2]int
}

func (f *fakeUI) Section(string)            {}
func (f *fakeUI) Begin(t string)            { f.mu.Lock(); defer f.mu.Unlock(); f.steps = append(f.steps, t) }
func (f *fakeUI) Progress(string, int, int) {}
func (f *fakeUI) Detail(string)             {}
func (f *fakeUI) DetailFor(step, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.details = append(f.details, step+": "+text)
}
func (f *fakeUI) ProgressFor(step, text string, cur, total int) {
	f.DetailFor(step, text)
}
func (f *fakeUI) Overall(done, total int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overall = [2]int{done, total}
}
func (f *fakeUI) Skipped(s string, a ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skipped = append(f.skipped, s)
}
func (f *fakeUI) Warn(string, ...any) {}
func (f *fakeUI) Info(s string, a ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.infos = append(f.infos, s)
}
func (f *fakeUI) End(step string, ok bool, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !ok {
		f.failed = append(f.failed, msg)
	}
}
func (f *fakeUI) CanPrompt() bool { return true }
func (f *fakeUI) Confirm(q, d string, def bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, q)
	if v, ok := f.answers[q]; ok {
		return v
	}
	return def
}

var errNope = errors.New("nope")

type fakeLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *fakeLog) Logf(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, format)
}

func newCtx() (*Context, *fakeUI) {
	u := &fakeUI{answers: map[string]bool{}}
	return &Context{UI: u, Log: &fakeLog{}}, u
}

func mkStep(id string, state State, apply func(*Context) error, needs ...string) *Step {
	return &Step{
		ID:    id,
		Name:  id,
		Needs: needs,
		Check: func(*Context) (State, error) { return state, nil },
		Apply: apply,
	}
}

func TestSatisfiedStepsAreSkipped(t *testing.T) {
	applied := false
	p := &Plan{Steps: []*Step{
		mkStep("a", Satisfied, func(*Context) error { applied = true; return nil }),
	}}
	ctx, ui := newCtx()
	if _, err := p.Run(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applied {
		t.Error("Apply ran for a step whose Check reported Satisfied")
	}
	if len(ui.skipped) != 1 {
		t.Errorf("expected 1 skip, got %d", len(ui.skipped))
	}
}

// Resume is the property the half-installed machine needed: steps already done
// are skipped, and the run picks up where it stopped.
func TestResumeSkipsCompletedWork(t *testing.T) {
	var ran []string
	p := &Plan{Steps: []*Step{
		mkStep("tex", Satisfied, func(*Context) error { ran = append(ran, "tex"); return nil }),
		mkStep("lyx", Satisfied, func(*Context) error { ran = append(ran, "lyx"); return nil }),
		mkStep("cfg", Pending, func(*Context) error { ran = append(ran, "cfg"); return nil }),
	}}
	ctx, _ := newCtx()
	if _, err := p.Run(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ran) != 1 || ran[0] != "cfg" {
		t.Errorf("expected only cfg to run, got %v", ran)
	}
}

func TestDryRunAppliesNothingButChecksEverything(t *testing.T) {
	checks := 0
	applies := 0
	p := &Plan{Steps: []*Step{
		{ID: "a", Name: "a",
			Check: func(*Context) (State, error) { checks++; return Pending, nil },
			Apply: func(*Context) error { applies++; return nil }},
		{ID: "b", Name: "b", Needs: []string{"a"},
			Check: func(*Context) (State, error) { checks++; return Pending, nil },
			Apply: func(*Context) error { applies++; return nil }},
	}}
	ctx, _ := newCtx()
	ctx.DryRun = true
	if _, err := p.Run(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applies != 0 {
		t.Errorf("dry run applied %d steps, expected 0", applies)
	}
	if checks != 2 {
		t.Errorf("dry run ran %d checks, expected 2", checks)
	}
}

// A dependent step must not run, and must not be reported as its own failure.
func TestDependentStepIsSkippedNotFailed(t *testing.T) {
	downstreamRan := false
	p := &Plan{Steps: []*Step{
		mkStep("lyx", Pending, func(*Context) error { return errors.New("winget unavailable") }),
		mkStep("cfg", Pending, func(*Context) error { downstreamRan = true; return nil }, "lyx"),
	}}
	ctx, ui := newCtx()
	results, err := p.Run(ctx)
	if err == nil {
		t.Fatal("expected the run to report an error")
	}
	if downstreamRan {
		t.Error("dependent step ran despite its prerequisite failing")
	}
	if len(ui.failed) != 1 {
		t.Errorf("expected exactly 1 failure reported, got %d", len(ui.failed))
	}
	if !errors.Is(results[1].Err, ErrSkippedDependency) {
		t.Errorf("dependent step should report ErrSkippedDependency, got %v", results[1].Err)
	}
}

func TestOptionalStepDoesNotFailTheRun(t *testing.T) {
	p := &Plan{Steps: []*Step{
		{ID: "opt", Name: "opt", Optional: true,
			Check: func(*Context) (State, error) { return Pending, nil },
			Apply: func(*Context) error { return errors.New("no admin rights") }},
		mkStep("after", Pending, func(*Context) error { return nil }),
	}}
	ctx, _ := newCtx()
	if _, err := p.Run(ctx); err != nil {
		t.Errorf("optional failure should not fail the run, got %v", err)
	}
}

func TestRollbackRunsInReverseAndReportsIrreversible(t *testing.T) {
	var undone []string
	p := &Plan{Steps: []*Step{
		{ID: "one", Name: "one",
			Check: func(*Context) (State, error) { return Pending, nil },
			Apply: func(*Context) error { return nil },
			Undo:  func(*Context) error { undone = append(undone, "one"); return nil }},
		{ID: "two", Name: "two",
			Check: func(*Context) (State, error) { return Pending, nil },
			Apply: func(*Context) error { return nil },
			Undo:  func(*Context) error { undone = append(undone, "two"); return nil }},
		{ID: "three", Name: "three",
			Check: func(*Context) (State, error) { return Pending, nil },
			Apply: func(*Context) error { return nil }}, // no Undo
	}}
	ctx, _ := newCtx()
	results, _ := p.Run(ctx)
	problems := p.Rollback(ctx, results)

	if len(undone) != 2 || undone[0] != "two" || undone[1] != "one" {
		t.Errorf("expected reverse order [two one], got %v", undone)
	}
	if len(problems) != 1 {
		t.Errorf("expected 1 irreversible step reported, got %d", len(problems))
	}
}

// Diagnose is the doctor. It must never apply anything.
func TestDiagnoseIsReadOnly(t *testing.T) {
	applied := false
	p := &Plan{Steps: []*Step{
		mkStep("a", Pending, func(*Context) error { applied = true; return nil }),
		mkStep("b", Satisfied, func(*Context) error { applied = true; return nil }),
	}}
	ctx, _ := newCtx()
	results := p.Diagnose(ctx)
	if applied {
		t.Error("Diagnose applied a step")
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Before != Pending || results[1].Before != Satisfied {
		t.Errorf("Diagnose reported wrong states: %v, %v", results[0].Before, results[1].Before)
	}
}

func TestValidateCatchesWiringMistakes(t *testing.T) {
	cases := map[string]*Plan{
		"duplicate id": {Steps: []*Step{
			mkStep("a", Pending, nil), mkStep("a", Pending, nil)}},
		"unknown dependency": {Steps: []*Step{
			mkStep("a", Pending, nil, "nope")}},
		"dependency declared later": {Steps: []*Step{
			mkStep("a", Pending, nil, "b"), mkStep("b", Pending, nil)}},
		"missing check": {Steps: []*Step{
			{ID: "a", Name: "a"}}},
	}
	for name, p := range cases {
		if err := p.Validate(); err == nil {
			t.Errorf("%s: expected Validate to reject this plan", name)
		}
	}
}

func TestContextCarriesDiscoveriesBetweenSteps(t *testing.T) {
	ctx, _ := newCtx()
	ctx.Set("lyx.root", `C:\Program Files\LyX 2.4`)
	got, ok := Get[string](ctx, "lyx.root")
	if !ok || got != `C:\Program Files\LyX 2.4` {
		t.Errorf("expected the recorded path back, got %q (ok=%v)", got, ok)
	}
	if _, ok := Get[int](ctx, "lyx.root"); ok {
		t.Error("Get should fail when the type does not match")
	}
}

// Concurrent steps must each own their status line.
//
// Before per-step scoping there was one shared detail slot, so two steps
// running at once overwrote each other continuously: a real install showed
// "installing LyX" and "mathtools [7/35]" alternating on the same line, which
// made the display actively misleading about what was happening.
func TestConcurrentStepsReportSeparately(t *testing.T) {
	// Both steps declare no dependencies, so the scheduler puts them in the
	// same wave and they genuinely run at the same time.
	bothStarted := make(chan struct{}, 2)
	proceed := make(chan struct{})

	report := func(text string) func(*Context) error {
		return func(c *Context) error {
			c.UI.Detail(text)
			bothStarted <- struct{}{}
			<-proceed
			return nil
		}
	}
	p := &Plan{Steps: []*Step{
		mkStep("alpha", Pending, report("doing alpha things")),
		mkStep("beta", Pending, report("doing beta things")),
	}}

	ctx, u := newCtx()
	go func() {
		<-bothStarted
		<-bothStarted
		close(proceed) // only release once both are inside Apply
	}()
	if _, err := p.Run(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := strings.Join(u.details, "\n")
	for _, want := range []string{"alpha: doing alpha things", "beta: doing beta things"} {
		if !strings.Contains(got, want) {
			t.Errorf("detail %q was not attributed to its step; got:\n%s", want, got)
		}
	}
}

// The display says "3/13 steps done", so the count has to reach the total and
// never overshoot it - including when steps are skipped for a failed dependency.
func TestOverallCountsEverySettledStep(t *testing.T) {
	p := &Plan{Steps: []*Step{
		mkStep("ok", Pending, func(*Context) error { return nil }),
		mkStep("bad", Pending, func(*Context) error { return errNope }),
		mkStep("downstream", Pending, func(*Context) error { return nil }, "bad"),
	}}
	ctx, u := newCtx()
	if _, err := p.Run(ctx); err == nil {
		t.Fatal("expected the failing step to be reported")
	}
	if want := [2]int{3, 3}; u.overall != want {
		t.Errorf("overall = %v, want %v (every step settles, including the skipped one)", u.overall, want)
	}
}
