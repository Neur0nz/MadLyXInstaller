// Package payload carries Kali's files, compiled into the binary.
//
// data/ is staged from the repository root at build time rather than being
// duplicated in version control. .gitattributes marks payload/** as -text so
// what ships is byte-identical to what the author published: git was
// previously normalising CRLF to LF on commit, which meant the Linux CI build
// embedded files 605 bytes smaller than the ones Windows shipped.
package payload

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:data
var data embed.FS

// Bind names the shortcut file matching a LyX series.
func Bind(series string) string { return "data/bind/madlyx-" + series + ".bind" }

// Read returns one embedded file.
func Read(name string) ([]byte, error) { return data.ReadFile(name) }

// WriteTo writes an embedded file to disk, creating parent directories.
func WriteTo(name, dest string) error {
	b, err := data.ReadFile(name)
	if err != nil {
		return fmt.Errorf("embedded file %s: %w", name, err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, b, 0o644)
}

// WriteTree copies an embedded directory to disk, flattened one level.
// Returns how many files were written.
func WriteTree(dir, destDir string) (int, error) {
	entries, err := data.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return 0, err
	}
	var n int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := WriteTo(dir+"/"+e.Name(), filepath.Join(destDir, e.Name())); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// List reports every embedded file and its size, used by the doctor to prove
// the payload survived compilation.
func List() (map[string]int, error) {
	out := map[string]int{}
	err := fs.WalkDir(data, "data", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := data.ReadFile(p)
		if err != nil {
			return err
		}
		out[strings.TrimPrefix(p, "data/")] = len(b)
		return nil
	})
	return out, err
}
