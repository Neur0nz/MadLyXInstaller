package winenv

import "testing"

// The PowerShell original compared version strings, so "2.10" sorted below
// "2.4" and a future LyX would have been handed the wrong shortcut file.
func TestBindSeriesSelection(t *testing.T) {
	cases := []struct {
		series     string
		wantSeries string
		wantExact  bool
	}{
		{"2.2", "2.3", false},
		{"2.3", "2.3", true},
		{"2.4", "2.4", true},
		{"2.5", "2.4", false},  // current stable, Feb 2026
		{"2.10", "2.4", false}, // the string-comparison trap
		{"3.0", "2.4", false},
	}
	for _, c := range cases {
		got, exact := LyX{Series: c.series}.BindSeries()
		if got != c.wantSeries || exact != c.wantExact {
			t.Errorf("LyX %s -> (%s, exact=%v), want (%s, exact=%v)",
				c.series, got, exact, c.wantSeries, c.wantExact)
		}
	}
}

func TestMutedColorsOnlyFor24AndLater(t *testing.T) {
	cases := map[string]bool{"2.2": false, "2.3": false, "2.4": true, "2.5": true, "2.10": true, "3.0": true}
	for series, want := range cases {
		if got := (LyX{Series: series}).WantsMutedColors(); got != want {
			t.Errorf("LyX %s WantsMutedColors = %v, want %v", series, got, want)
		}
	}
}

func TestUserDirName(t *testing.T) {
	if got := (LyX{Series: "2.4"}).UserDirName(); got != "LyX2.4" {
		t.Errorf("UserDirName = %q, want LyX2.4", got)
	}
}

func TestHasHebrew(t *testing.T) {
	cases := map[string]bool{
		`C:\Users\nadav\Documents`: false,
		`C:\Users\Jos` + "\u00e9":  false, // accented Latin is not Hebrew
		"":                         false,
		`C:\Users\` + "\u05e0\u05d3\u05d1":                    true, // nadav
		`C:\Studies\` + "\u05d0\u05d9\u05e0\u05e4\u05d9" + `\ex1`: true,
		"D:\\" + "\u05ea\u05e8\u05d2\u05d9\u05dd":               true, // final letters
		"\ufb2a":                                                true, // presentation form
	}
	for in, want := range cases {
		if got := HasHebrew(in); got != want {
			t.Errorf("HasHebrew(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestVersionFromName(t *testing.T) {
	cases := map[string]string{
		"LyX 2.4.4": "2.4.4",
		"LyX 2.3.7": "2.3.7",
		"LyXFoo":    "",
	}
	for in, want := range cases {
		if got := versionFromName(in); got != want {
			t.Errorf("versionFromName(%q) = %q, want %q", in, got, want)
		}
	}
}
