package step

import "testing"

// A healthy installation must read as healthy. Optional steps are offers the
// user may have declined, and the test compile verifies rather than configures
// so it is never Satisfied - counting either as a fault made `doctor` report
// three problems on a machine that had just installed cleanly.
func TestDoctorSeparatesFaultsFromDeclinedOffers(t *testing.T) {
	p := &Plan{Steps: []*Step{
		{ID: "core", Name: "core",
			Check: func(*Context) (State, error) { return Satisfied, nil }},
		{ID: "offer", Name: "offer", Optional: true,
			Check: func(*Context) (State, error) { return Pending, nil }},
		{ID: "verify", Name: "verify", Optional: true,
			Check: func(*Context) (State, error) { return Pending, nil }},
	}}
	ctx, _ := newCtx()
	results := p.Diagnose(ctx)

	var faults, offers int
	for _, r := range results {
		if r.Before == Satisfied {
			continue
		}
		if r.Step.Optional {
			offers++
			continue
		}
		faults++
	}
	if faults != 0 {
		t.Errorf("healthy install reported %d faults, want 0", faults)
	}
	if offers != 2 {
		t.Errorf("expected 2 declined offers, got %d", offers)
	}
}

// A genuinely broken installation must still be reported.
func TestDoctorReportsRealFaults(t *testing.T) {
	p := &Plan{Steps: []*Step{
		{ID: "core", Name: "core",
			Check: func(*Context) (State, error) { return Pending, nil }},
		{ID: "offer", Name: "offer", Optional: true,
			Check: func(*Context) (State, error) { return Pending, nil }},
	}}
	ctx, _ := newCtx()
	var faults int
	for _, r := range p.Diagnose(ctx) {
		if r.Before != Satisfied && !r.Step.Optional {
			faults++
		}
	}
	if faults != 1 {
		t.Errorf("expected the broken required step to be reported, got %d faults", faults)
	}
}
