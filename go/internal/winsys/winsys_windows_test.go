//go:build windows

package winsys

import "testing"

// These functions touch the real machine, so the tests assert only what can be
// checked without changing anything. The write paths are covered by the
// step-level tests and by CI's clean-runner install, not here - a unit test
// that edited Defender configuration would be worse than no test.

func TestIsAdminDoesNotPanic(t *testing.T) {
	_ = IsAdmin()
}

func TestDefenderExclusionsReadIsStable(t *testing.T) {
	first, err := DefenderExclusions()
	if err != nil {
		t.Skip("Defender is unavailable or managed on this machine")
	}
	second, err := DefenderExclusions()
	if err != nil {
		t.Fatalf("second read failed: %v", err)
	}
	if len(first) != len(second) {
		t.Error("two consecutive reads disagreed; the check is not deterministic")
	}
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
