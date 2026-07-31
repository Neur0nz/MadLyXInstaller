package lyxcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A realistic pre-existing preferences file: some keys we manage, some we do
// not. Both cases matter - ours must be replaced, theirs must survive.
const existingPrefs = `# LyX 2.4.4 generated this file.
Format 36

\gui_language hebrew
\kbmap false
\screen_font_roman "DejaVu Serif"
\visual_cursor false
\set_color "green" "#00ff00"
\user_name "Existing Student"
\autosave 60
`

func madlyxSettings() *Settings {
	s := NewSettings()
	s.Set("gui_language", "english")
	s.Set("kbmap", "true")
	s.SetQuoted("kbmap_primary", "null")
	s.SetQuoted("kbmap_secondary", "hebrew")
	s.Set("visual_cursor", "true")
	s.Set("scroll_below_document", "true")
	s.SetQuoted("bind_file", "cua")
	s.SetQuoted("path_prefix", `C:\Program Files\MiKTeX\miktex\bin\x64`)
	s.Set("preview", "on")
	s.Set("autosave", "300")
	s.SetColor("green", "#b5bd68")
	s.SetColor("red", "#cc6666")
	return s
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "preferences")
	if content != "" {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The property the PowerShell version got wrong by using append mode.
func TestApplyIsIdempotent(t *testing.T) {
	p := writeTemp(t, existingPrefs)
	s := madlyxSettings()

	for i := 0; i < 5; i++ {
		if _, err := Apply(p, s); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	got := read(t, p)

	for _, k := range s.Keys() {
		if n := strings.Count(got, "\\"+k+" "); n != 1 {
			t.Errorf("key %q appears %d times after 5 runs, want 1", k, n)
		}
	}
	if n := strings.Count(got, blockStart); n != 1 {
		t.Errorf("block start appears %d times, want 1", n)
	}
	if n := strings.Count(got, `\set_color "green"`); n != 1 {
		t.Errorf("set_color green appears %d times, want 1", n)
	}
}

func TestApplyPreservesUnmanagedSettings(t *testing.T) {
	p := writeTemp(t, existingPrefs)
	if _, err := Apply(p, madlyxSettings()); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)

	for _, want := range []string{
		`\screen_font_roman "DejaVu Serif"`,
		`\user_name "Existing Student"`,
		"Format 36",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lost an unmanaged setting: %q", want)
		}
	}
}

func TestApplyOverridesManagedSettings(t *testing.T) {
	p := writeTemp(t, existingPrefs)
	if _, err := Apply(p, madlyxSettings()); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)

	for _, want := range []string{`\gui_language english`, `\kbmap true`, `\visual_cursor true`, `\autosave 300`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing override %q", want)
		}
	}
	for _, gone := range []string{`\gui_language hebrew`, `\kbmap false`, `\autosave 60`, "#00ff00"} {
		if strings.Contains(got, gone) {
			t.Errorf("stale value survived: %q", gone)
		}
	}
}

func TestPathsUseForwardSlashes(t *testing.T) {
	p := writeTemp(t, "")
	if _, err := Apply(p, madlyxSettings()); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	if !strings.Contains(got, `\path_prefix "C:/Program Files/MiKTeX/miktex/bin/x64"`) {
		t.Errorf("path not normalised to forward slashes:\n%s", got)
	}
	if strings.Contains(got, `C:\Program Files\MiKTeX`) {
		t.Error("backslashes survived into a quoted LyX string")
	}
}

func TestNoByteOrderMark(t *testing.T) {
	p := writeTemp(t, "")
	if _, err := Apply(p, madlyxSettings()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		t.Error("wrote a UTF-8 BOM; LyX does not expect one")
	}
}

// IsApplied is both the doctor's check and the step engine's Check. If it
// disagreed with Apply the two would drift, which is what happened when the
// PowerShell version wrote that knowledge out twice.
func TestIsAppliedAgreesWithApply(t *testing.T) {
	p := writeTemp(t, existingPrefs)
	s := madlyxSettings()

	if ok, _ := IsApplied(p, s); ok {
		t.Error("reported applied before Apply ran")
	}
	if _, err := Apply(p, s); err != nil {
		t.Fatal(err)
	}
	if ok, err := IsApplied(p, s); !ok || err != nil {
		t.Errorf("reported not applied immediately after Apply (err=%v)", err)
	}

	// A changed value must be detected as no longer applied.
	s2 := madlyxSettings()
	s2.Set("autosave", "999")
	if ok, _ := IsApplied(p, s2); ok {
		t.Error("reported applied for settings that differ from the file")
	}
}

func TestIsAppliedOnMissingFile(t *testing.T) {
	ok, err := IsApplied(filepath.Join(t.TempDir(), "nope"), madlyxSettings())
	if err != nil || ok {
		t.Errorf("missing file should be not-applied with no error, got ok=%v err=%v", ok, err)
	}
}

// Backups must never overwrite each other, even within the same second.
func TestBackupsNeverCollide(t *testing.T) {
	p := writeTemp(t, existingPrefs)
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		b, err := BackupFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if seen[b] {
			t.Fatalf("backup %q was produced twice, losing the earlier one", b)
		}
		seen[b] = true
	}
	if len(seen) != 5 {
		t.Errorf("expected 5 distinct backups, got %d", len(seen))
	}
}

func TestBackupPreservesOriginalContent(t *testing.T) {
	p := writeTemp(t, existingPrefs)
	backup, err := Apply(p, madlyxSettings())
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("Apply did not back up an existing file")
	}
	if got := read(t, backup); got != existingPrefs {
		t.Error("backup does not match the original file byte for byte")
	}
}

func TestLatestBackupFindsMostRecent(t *testing.T) {
	p := writeTemp(t, existingPrefs)
	var last string
	for i := 0; i < 3; i++ {
		last, _ = BackupFile(p)
	}
	got, ok := LatestBackup(p)
	if !ok || got != last {
		t.Errorf("LatestBackup = %q (ok=%v), want %q", got, ok, last)
	}
}

func TestApplyCreatesFileWhenMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "preferences")
	backup, err := Apply(p, madlyxSettings())
	if err != nil {
		t.Fatal(err)
	}
	if backup != "" {
		t.Error("backed up a file that did not exist")
	}
	if ok, _ := IsApplied(p, madlyxSettings()); !ok {
		t.Error("freshly created file does not read back as applied")
	}
}
