package step

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Waves must be derived from the declared dependencies, so nothing has to be
// marked parallel by hand and nothing can be scheduled before what it needs.
func TestWavesFollowDependencies(t *testing.T) {
	p := &Plan{Steps: []*Step{
		mkStep("preflight", Pending, nil),
		mkStep("tex", Pending, nil, "preflight"),
		mkStep("lyx", Pending, nil, "tex"),
		mkStep("texpackages", Pending, nil, "tex"),
		mkStep("defender", Pending, nil, "tex"),
		mkStep("userdir", Pending, nil, "lyx"),
		mkStep("settings", Pending, nil, "userdir", "tex"),
	}}
	waves := p.Waves()

	if len(waves) != 5 {
		t.Fatalf("expected 5 waves, got %d", len(waves))
	}
	if len(waves[0]) != 1 || waves[0][0].ID != "preflight" {
		t.Errorf("wave 0 should be preflight alone, got %v", idsOf(waves[0]))
	}
	if len(waves[1]) != 1 || waves[1][0].ID != "tex" {
		t.Errorf("wave 1 should be tex alone, got %v", idsOf(waves[1]))
	}
	// The win: three independent steps sharing one prerequisite.
	got := idsOf(waves[2])
	if len(got) != 3 {
		t.Errorf("wave 2 should hold lyx, texpackages and defender together, got %v", got)
	}
}

// A step must never appear before something it needs.
func TestWavesNeverSchedulePrerequisitesLate(t *testing.T) {
	p := &Plan{Steps: []*Step{
		mkStep("a", Pending, nil),
		mkStep("b", Pending, nil, "a"),
		mkStep("c", Pending, nil, "b"),
		mkStep("d", Pending, nil, "a"),
	}}
	depth := map[string]int{}
	for i, wave := range p.Waves() {
		for _, s := range wave {
			depth[s.ID] = i
		}
	}
	for _, s := range p.Steps {
		for _, need := range s.Needs {
			if depth[need] >= depth[s.ID] {
				t.Errorf("%s (wave %d) is not after its prerequisite %s (wave %d)",
					s.ID, depth[s.ID], need, depth[need])
			}
		}
	}
}

// Independent steps must genuinely overlap, not merely be grouped.
func TestIndependentStepsRunAtTheSameTime(t *testing.T) {
	var concurrent, peak int32
	track := func(*Context) error {
		n := atomic.AddInt32(&concurrent, 1)
		for {
			old := atomic.LoadInt32(&peak)
			if n <= old || atomic.CompareAndSwapInt32(&peak, old, n) {
				break
			}
		}
		time.Sleep(60 * time.Millisecond)
		atomic.AddInt32(&concurrent, -1)
		return nil
	}
	p := &Plan{Steps: []*Step{
		mkStep("root", Pending, track),
		mkStep("a", Pending, track, "root"),
		mkStep("b", Pending, track, "root"),
		mkStep("c", Pending, track, "root"),
	}}
	ctx, _ := newCtx()
	if _, err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if peak < 3 {
		t.Errorf("peak concurrency was %d, expected 3 independent steps to overlap", peak)
	}
}

// Steps that may prompt must run alone: two questions at once would queue with
// no way for the user to tell which one they were answering.
func TestInteractiveStepsRunAlone(t *testing.T) {
	var concurrent, peak int32
	track := func(*Context) error {
		n := atomic.AddInt32(&concurrent, 1)
		for {
			old := atomic.LoadInt32(&peak)
			if n <= old || atomic.CompareAndSwapInt32(&peak, old, n) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		atomic.AddInt32(&concurrent, -1)
		return nil
	}
	p := &Plan{Steps: []*Step{
		mkStep("root", Pending, track),
		{ID: "ask1", Name: "ask1", Needs: []string{"root"}, Interactive: true,
			Check: func(*Context) (State, error) { return Pending, nil }, Apply: track},
		{ID: "ask2", Name: "ask2", Needs: []string{"root"}, Interactive: true,
			Check: func(*Context) (State, error) { return Pending, nil }, Apply: track},
	}}
	ctx, _ := newCtx()
	if _, err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if peak > 1 {
		t.Errorf("peak concurrency was %d; interactive steps must not overlap", peak)
	}
}

// However the scheduler interleaves them, the summary must read in the order
// the steps were declared.
func TestResultsComeBackInDeclarationOrder(t *testing.T) {
	slow := func(d time.Duration) func(*Context) error {
		return func(*Context) error { time.Sleep(d); return nil }
	}
	p := &Plan{Steps: []*Step{
		mkStep("root", Pending, slow(0)),
		mkStep("slow", Pending, slow(80*time.Millisecond), "root"),
		mkStep("fast", Pending, slow(0), "root"),
	}}
	ctx, _ := newCtx()
	results, err := p.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"root", "slow", "fast"}
	for i, w := range want {
		if results[i].Step.ID != w {
			t.Errorf("result %d is %q, want %q (declaration order)", i, results[i].Step.ID, w)
		}
	}
}

// Concurrent steps share the Context, so recording discoveries must be safe.
func TestConcurrentStateWritesAreSafe(t *testing.T) {
	p := &Plan{Steps: []*Step{mkStep("root", Pending, nil)}}
	for i := 0; i < 20; i++ {
		id := string(rune('a' + i))
		p.Steps = append(p.Steps, &Step{
			ID: id, Name: id, Needs: []string{"root"},
			Check: func(*Context) (State, error) { return Pending, nil },
			Apply: func(c *Context) error { c.Set(id, id); return nil },
		})
	}
	ctx, _ := newCtx()
	if _, err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		id := string(rune('a' + i))
		if v, ok := Get[string](ctx, id); !ok || v != id {
			t.Errorf("lost the value recorded by step %q", id)
		}
	}
}

// A failure in one concurrent step must not stop its siblings.
func TestSiblingsFinishWhenOneFails(t *testing.T) {
	var mu sync.Mutex
	var ran []string
	record := func(name string, fail bool) func(*Context) error {
		return func(*Context) error {
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			ran = append(ran, name)
			mu.Unlock()
			if fail {
				return errNope
			}
			return nil
		}
	}
	p := &Plan{Steps: []*Step{
		mkStep("root", Pending, nil),
		mkStep("bad", Pending, record("bad", true), "root"),
		mkStep("good1", Pending, record("good1", false), "root"),
		mkStep("good2", Pending, record("good2", false), "root"),
	}}
	ctx, _ := newCtx()
	if _, err := p.Run(ctx); err == nil {
		t.Error("expected the failing step to be reported")
	}
	if len(ran) != 3 {
		t.Errorf("expected all 3 siblings to run, got %v", ran)
	}
}

func idsOf(steps []*Step) []string {
	out := make([]string, 0, len(steps))
	for _, s := range steps {
		out = append(out, s.ID)
	}
	return out
}
