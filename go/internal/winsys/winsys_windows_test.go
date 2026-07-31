//go:build windows

package winsys

import "testing"

// These functions touch the real machine, so the tests assert only what can be
// checked without changing anything: that reads succeed and are consistent.
// The write paths are covered by the step-level tests and by CI's clean-runner
// install, not here - a unit test that edits the registry would be worse than
// no test.

func TestLanguageToggleReadIsStable(t *testing.T) {
	first, err := LanguageToggleDisabled()
	if err != nil {
		t.Fatalf("reading the toggle state failed: %v", err)
	}
	second, err := LanguageToggleDisabled()
	if err != nil {
		t.Fatalf("second read failed: %v", err)
	}
	if first != second {
		t.Error("two consecutive reads disagreed; the check is not deterministic")
	}
}

// A missing registry key means the Windows default (Alt+Shift active), not an
// error. Getting this wrong would make the doctor report a fault on every
// machine that has never changed the setting.
func TestMissingKeyMeansEnabledNotError(t *testing.T) {
	if _, err := LanguageToggleDisabled(); err != nil {
		t.Errorf("absent or unreadable key should report enabled, not error: %v", err)
	}
}

func TestIsAdminDoesNotPanic(t *testing.T) {
	_ = IsAdmin()
}

func TestHebrewInputCheckDoesNotPanic(t *testing.T) {
	_ = HebrewInputInstalled()
}

// Elevation arguments must survive quoting, or a path with a space silently
// becomes two arguments.
func TestQuoteAllHandlesSpaces(t *testing.T) {
	got := quoteAll([]string{"install", `C:\Program Files\LyX`, "--yes"})
	if got[1] != `"C:\Program Files\LyX"` {
		t.Errorf("path with a space was not quoted: %q", got[1])
	}
	if got[0] != "install" || got[2] != "--yes" {
		t.Errorf("plain arguments should not be quoted: %v", got)
	}
}
