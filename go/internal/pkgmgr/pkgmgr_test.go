package pkgmgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls [][]string
	fail  map[string]bool
	emit  []string // lines handed to the progress callback
	env   []string // extra environment variables the caller asked for
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	return f.RunWith(ctx, RunOpts{}, name, args...)
}

func (f *fakeRunner) RunWith(_ context.Context, o RunOpts, name string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	f.env = append(f.env, o.Env...)
	if o.OnLine != nil {
		for _, l := range f.emit {
			o.OnLine(l)
		}
	}
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

// winget's own output is unreadable as progress: legal notices, block
// characters, and a download bar redrawn with carriage returns. Only the lines
// that say something a user can act on should reach the display.
func TestWingetProgressIsTranslatedNotEchoed(t *testing.T) {
	r := &fakeRunner{emit: []string{
		"Found LyX 2.4.4 [LyX.LyX]",
		"This application is licensed to you by its owner.",
		"Microsoft is not responsible for, nor does it grant any licenses to, third-party packages.",
		"  ██████████████████████  41.2 MB / 57.6 MB",
		"Successfully verified installer hash",
		"Starting package install...",
	}}
	var seen []string
	err := WingetInstallOpts(context.Background(), r, "LyX.LyX", WingetOpts{
		Progress: func(p string) { seen = append(seen, p) },
	})
	if err != nil {
		t.Fatal(err)
	}

	joined := strings.Join(seen, " | ")
	for _, want := range []string{"downloading 41.2 MB of 57.6 MB", "verified the download", "running the installer"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
	for _, unwanted := range []string{"Microsoft is not responsible", "█", "licensed to you"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("noise %q reached the display: %q", unwanted, joined)
		}
	}
}

// winget rewrites its download line with \r. Splitting on newlines alone means
// the whole download arrives as one line after it has already finished.
func TestLineWriterBreaksOnCarriageReturns(t *testing.T) {
	var got []string
	w := &lineWriter{emit: func(s string) { got = append(got, s) }}
	w.Write([]byte("10 MB / 57 MB\r20 MB / 57 MB\r"))
	w.Write([]byte("30 MB / 57 MB\ndone\n"))

	want := []string{"10 MB / 57 MB", "20 MB / 57 MB", "30 MB / 57 MB", "done"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

// A command that emits no line break at all must not grow the buffer forever.
func TestLineWriterBoundsAnUnbrokenStream(t *testing.T) {
	w := &lineWriter{emit: func(string) {}}
	for i := 0; i < 100; i++ {
		w.Write([]byte(strings.Repeat("x", 1000)))
	}
	if len(w.buf) > 8192 {
		t.Errorf("buffer grew to %d bytes", len(w.buf))
	}
}

// /CurrentUser decides where LyX installs; it does not stop Windows asking for
// permission first. A real run landed in %LOCALAPPDATA% with HKCU only and
// still showed a UAC dialog, because the installer requests the highest
// available privileges from its manifest. RunAsInvoker is what suppresses that,
// so it must accompany the per-user switch.
func TestPerUserInstallSuppressesElevation(t *testing.T) {
	r := &fakeRunner{}
	err := WingetInstallOpts(context.Background(), r, "LyX.LyX", WingetOpts{
		Custom: []string{"/CurrentUser"},
		Env:    []string{RunAsInvoker},
	})
	if err != nil {
		t.Fatal(err)
	}
	if call := strings.Join(r.calls[0], " "); !strings.Contains(call, "--custom /CurrentUser") {
		t.Errorf("per-user switch not passed to the installer: %s", call)
	}
	if !strings.Contains(strings.Join(r.env, " "), "__COMPAT_LAYER=RunAsInvoker") {
		t.Errorf("RunAsInvoker not set, so Windows would still prompt: %v", r.env)
	}
}

// The download must not run anything, and must ask winget for a directory we
// control - launching via winget is precisely what stopped RunAsInvoker working.
func TestWingetDownloadDoesNotInstall(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "LyX_2.4.4.1_X64_nullsoft_en-US.exe"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// winget writes a manifest beside the installer; it must not be mistaken
	// for the thing to run.
	if err := os.WriteFile(filepath.Join(dir, "LyX_2.4.4.1_X64_nullsoft_en-US.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &fakeRunner{}
	got, err := WingetDownload(context.Background(), r, "LyX.LyX", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(got) != ".exe" {
		t.Errorf("picked %q, which is not the installer", got)
	}
	call := strings.Join(r.calls[0], " ")
	if !strings.Contains(call, "winget download") {
		t.Errorf("did not use the download verb: %s", call)
	}
	if strings.Contains(call, "install") {
		t.Errorf("download must not install: %s", call)
	}
}

// The whole point of warming is that it happens before LyX, not after, and that
// a failure never blocks the install - it is a time optimisation, not a step.
func TestWarmMiKTeXBuildsBothCachesAndSurvivesFailure(t *testing.T) {
	r := &fakeRunner{fail: map[string]bool{"--update-fndb": true}}
	WarmMiKTeX(context.Background(), r, `C:\bin`, nil)

	joined := strings.Join(r.calls[0], " ") + " " + strings.Join(r.calls[len(r.calls)-1], " ")
	for _, want := range []string{"miktex-fc-cache.exe", "--update-fndb"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warming did not run %q: %v", want, r.calls)
		}
	}
	if len(r.calls) != 2 {
		t.Errorf("expected both caches to be attempted, got %d calls", len(r.calls))
	}
}

// Auto-install has to be switchable, because leaving it on during the LyX
// install is what made that step take eight and a half minutes.
func TestAutoInstallCanBePausedAndRestored(t *testing.T) {
	r := &fakeRunner{}
	if err := SetMiKTeXAutoInstall(context.Background(), r, `C:\bin`, false); err != nil {
		t.Fatal(err)
	}
	if err := EnableMiKTeXAutoInstall(context.Background(), r, `C:\bin`); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.calls[0], " "); !strings.Contains(got, "[MPM]AutoInstall=0") {
		t.Errorf("pause did not set 0: %s", got)
	}
	if got := strings.Join(r.calls[1], " "); !strings.Contains(got, "[MPM]AutoInstall=1") {
		t.Errorf("restore did not set 1: %s", got)
	}
}
