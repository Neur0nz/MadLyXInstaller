package steps

import (
	"strings"
	"testing"

	"github.com/Neur0nz/MadLyXInstaller/go/internal/payload"
	"github.com/Neur0nz/MadLyXInstaller/go/internal/step"
)

func testOptions() Options {
	return Options{TeXDistribution: "auto", Runner: nil}
}

// Validate catches duplicate IDs and dependencies declared out of order, so a
// wiring mistake fails before anything touches the machine.
func TestPlanIsWellFormed(t *testing.T) {
	if err := Build(testOptions()).Validate(); err != nil {
		t.Fatalf("default plan is invalid: %v", err)
	}
	opts := testOptions()
	opts.SkipSystemSteps = true
	opts.SkipSmokeTest = true
	if err := Build(opts).Validate(); err != nil {
		t.Fatalf("reduced plan is invalid: %v", err)
	}
}

func TestSkipFlagsRemoveTheRightSteps(t *testing.T) {
	full := ids(Build(testOptions()))
	if !contains2(full, "smoketest") || !contains2(full, "defender") {
		t.Fatalf("default plan is missing expected steps: %v", full)
	}

	o := testOptions()
	o.SkipSmokeTest = true
	o.SkipSystemSteps = true
	reduced := ids(Build(o))
	for _, gone := range []string{"smoketest", "defender"} {
		if contains2(reduced, gone) {
			t.Errorf("%q should have been removed: %v", gone, reduced)
		}
	}
	// The core work must survive the skips.
	for _, kept := range []string{"tex", "lyx", "settings", "shortcuts", "templates"} {
		if !contains2(reduced, kept) {
			t.Errorf("%q should not have been removed: %v", kept, reduced)
		}
	}
}

// Every step that changes something outside LyX must be Optional, so a failure
// there cannot abort an otherwise good install.
func TestSystemStepsAreOptional(t *testing.T) {
	for _, s := range Build(testOptions()).Steps {
		if s.ID == "defender" || s.ID == "culmus" || s.ID == "smoketest" || s.ID == "reconfigure" {
			if !s.Optional {
				t.Errorf("step %q should be Optional", s.ID)
			}
		}
	}
}

// Checks run during a dry run and in the doctor, so none may mutate.
func TestEveryStepHasACheck(t *testing.T) {
	for _, s := range Build(testOptions()).Steps {
		if s.Check == nil {
			t.Errorf("step %q has no Check", s.ID)
		}
		if s.Name == "" {
			t.Errorf("step %q has no user-facing name", s.ID)
		}
	}
}

// Steps that change the user's configuration should be reversible.
func TestConfigStepsCanBeUndone(t *testing.T) {
	want := map[string]bool{"settings": true, "defender": true}
	for _, s := range Build(testOptions()).Steps {
		if want[s.ID] && s.Undo == nil {
			t.Errorf("step %q changes configuration but has no Undo", s.ID)
		}
	}
}

// Hebrew in the profile path is the guide's leading cause of failure, and
// TeX Live copes with it better than MiKTeX.
func TestDistroChoice(t *testing.T) {
	if got := chooseDistro("miktex"); got != "miktex" {
		t.Errorf("explicit miktex ignored, got %q", got)
	}
	if got := chooseDistro("texlive"); got != "texlive" {
		t.Errorf("explicit texlive ignored, got %q", got)
	}
	// "auto" depends on the machine's profile path, so only assert it is valid.
	if got := chooseDistro("auto"); got != "miktex" && got != "texlive" {
		t.Errorf("auto produced %q", got)
	}
}

// The payload must survive compilation, or the installer ships nothing.
func TestPayloadIsEmbedded(t *testing.T) {
	files, err := payload.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 12 {
		t.Errorf("expected 12 embedded files, got %d: %v", len(files), files)
	}
	// Byte counts must match what Kali published: .gitattributes marks the
	// payload as binary so git cannot rewrite line endings.
	expect := map[string]int{
		"bind/madlyx-2.3.bind":              38651,
		"bind/madlyx-2.4.bind":              38268,
		"macros/madlyx-macros-he.lyx":       16796,
		"templates/01-standard-minimal.lyx": 6252,
	}
	for name, size := range expect {
		if got := files[name]; got != size {
			t.Errorf("%s embedded as %d bytes, want %d (line endings rewritten?)", name, got, size)
		}
	}
}

func TestBindPathsResolve(t *testing.T) {
	for _, series := range []string{"2.3", "2.4"} {
		if _, err := payload.Read(payload.Bind(series)); err != nil {
			t.Errorf("bind file for %s not embedded: %v", series, err)
		}
	}
}

func TestDiagnoseFailureMessagesNameTheFix(t *testing.T) {
	cases := map[string]string{
		"! LaTeX Error: File `culmus.sty' not found.":  "guide p.21",
		"! LaTeX Error: File `cp1255.def' not found.":  "guide p.25",
	}
	for output, wantFragment := range cases {
		got := diagnoseCompile(output, nil)
		if !strings.Contains(got, wantFragment) {
			t.Errorf("diagnose(%q) = %q, expected it to mention %q", output, got, wantFragment)
		}
	}
}

func ids(p *step.Plan) []string {
	out := make([]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		out = append(out, s.ID)
	}
	return out
}

func contains2(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
