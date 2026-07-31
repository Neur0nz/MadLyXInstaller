package pkgmgr

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls [][]string
	fail  map[string]bool
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	for _, a := range args {
		if f.fail[a] {
			return "", errors.New("nope")
		}
	}
	return "ok", nil
}

// The winget accident that installed an unrelated Store app came from a bare,
// unquoted query. Every call this package makes must pin the package by ID.
func TestWingetAlwaysUsesExactID(t *testing.T) {
	r := &fakeRunner{}
	if err := WingetInstall(context.Background(), r, "LyX.LyX"); err != nil {
		t.Fatal(err)
	}
	call := strings.Join(r.calls[0], " ")
	for _, required := range []string{"--id LyX.LyX", "--exact", "--accept-source-agreements", "--accept-package-agreements"} {
		if !strings.Contains(call, required) {
			t.Errorf("winget call missing %q: %s", required, call)
		}
	}
}

func TestInstallTeXPackagesReportsProgressForEvery(t *testing.T) {
	r := &fakeRunner{}
	var seen []string
	var lastTotal int
	err := InstallTeXPackages(context.Background(), r, "miktex", `C:\bin`,
		func(name string, i, total int) { seen = append(seen, name); lastTotal = total })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seen) != len(HebrewPackages) {
		t.Errorf("progress reported for %d packages, want %d", len(seen), len(HebrewPackages))
	}
	if lastTotal != len(HebrewPackages) {
		t.Errorf("total reported as %d, want %d", lastTotal, len(HebrewPackages))
	}
}

// One bad package name must not abort the rest, but must still be reported.
func TestInstallTeXPackagesContinuesPastFailures(t *testing.T) {
	r := &fakeRunner{fail: map[string]bool{"mhchem": true, "wasysym": true}}
	err := InstallTeXPackages(context.Background(), r, "miktex", `C:\bin`, nil)
	if err == nil {
		t.Fatal("expected the failures to be reported")
	}
	if !strings.Contains(err.Error(), "mhchem") || !strings.Contains(err.Error(), "wasysym") {
		t.Errorf("error should name both failures, got %v", err)
	}
	if len(r.calls) != len(HebrewPackages) {
		t.Errorf("stopped after %d packages, should have tried all %d", len(r.calls), len(HebrewPackages))
	}
}

func TestHebrewPackagesCoverTheEssentials(t *testing.T) {
	// scheme-basic omits these, which is how students end up in the guide's
	// troubleshooting chapter.
	for _, want := range []string{"babel-hebrew", "culmus", "preview", "dvipng", "mathtools", "relsize", "stmaryrd"} {
		found := false
		for _, p := range HebrewPackages {
			if p == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("essential package %q missing from the list", want)
		}
	}
}

func TestContextCancellationStopsTheLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &fakeRunner{}
	cancel()
	if err := InstallTeXPackages(ctx, r, "miktex", `C:\bin`, nil); err == nil {
		t.Error("expected cancellation to be reported")
	}
}
