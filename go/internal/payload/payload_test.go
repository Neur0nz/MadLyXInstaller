package payload

import (
	"os"
	"path/filepath"
	"testing"
)

// The payload is redistributed verbatim from Kali's originals. These sizes are
// what he published; .gitattributes marks payload/** as -text so git cannot
// rewrite line endings. A CI build once embedded files 605 bytes smaller than
// the Windows build, and only a byte-count comparison revealed it.
var published = map[string]int{
	"bind/madlyx-2.3.bind":              38651,
	"bind/madlyx-2.4.bind":              38268,
	"macros/madlyx-macros-he.lyx":       16796,
	"macros/madlyx-macros-en.lyx":       21130,
	"templates/01-standard-minimal.lyx": 6252,
	"templates/02-hebrew-article.lyx":   5425,
	"templates/03-standard-fancy.lyx":   9915,
	"templates/04-two-column.lyx":       6748,
	"templates/05-english.lyx":          6957,
}

func TestEmbeddedFilesMatchWhatWasPublished(t *testing.T) {
	files, err := List()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range published {
		got, ok := files[name]
		if !ok {
			t.Errorf("%s is not embedded", name)
			continue
		}
		if got != want {
			t.Errorf("%s embedded as %d bytes, want %d - line endings rewritten?", name, got, want)
		}
	}
}

func TestEverythingNeededIsEmbedded(t *testing.T) {
	files, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 11 {
		t.Errorf("expected 11 files, got %d: %v", len(files), files)
	}
	for _, needed := range []string{
		"preamble/madlyx-preamble.tex",
		"smoketest/smoketest.lyx",
	} {
		if _, ok := files[needed]; !ok {
			t.Errorf("%s is not embedded", needed)
		}
	}
}

func TestBindResolvesForBothSeries(t *testing.T) {
	for _, series := range []string{"2.3", "2.4"} {
		b, err := Read(Bind(series))
		if err != nil {
			t.Errorf("bind %s: %v", series, err)
			continue
		}
		if len(b) == 0 {
			t.Errorf("bind %s is empty", series)
		}
	}
	if _, err := Read(Bind("9.9")); err == nil {
		t.Error("a nonexistent series should fail rather than return nothing")
	}
}

func TestWriteToRoundTrips(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "nested", "user.bind")
	if err := WriteTo(Bind("2.4"), dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := Read(Bind("2.4"))
	if len(got) != len(want) {
		t.Errorf("wrote %d bytes, embedded is %d", len(got), len(want))
	}
}

func TestWriteTreeWritesEveryTemplate(t *testing.T) {
	dir := t.TempDir()
	n, err := WriteTree("data/templates", dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("wrote %d templates, want 5", n)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 5 {
		t.Errorf("%d files on disk, want 5", len(entries))
	}
}

func TestMissingFileIsAnError(t *testing.T) {
	if err := WriteTo("data/nope.txt", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("expected an error for a file that is not embedded")
	}
}
