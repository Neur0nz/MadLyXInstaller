// Package lyxcfg writes LyX configuration idempotently.
//
// Every key name here was verified against LyX's own LyXRC.cpp tag table
// rather than recalled. That check caught \gui_language, which had been
// written as \ui_language - a key LyX silently ignores.
package lyxcfg

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	blockStart = "### BEGIN MadLyXInstaller - do not edit inside this block"
	blockEnd   = "### END MadLyXInstaller"
)

// Settings is an ordered list of LyX preference assignments.
type Settings struct {
	keys   []string
	values map[string]string
	colors map[string]string
}

// NewSettings creates an empty set.
func NewSettings() *Settings {
	return &Settings{values: map[string]string{}, colors: map[string]string{}}
}

// Set records a key, preserving insertion order so the written block is stable.
func (s *Settings) Set(key, value string) *Settings {
	if _, seen := s.values[key]; !seen {
		s.keys = append(s.keys, key)
	}
	s.values[key] = value
	return s
}

// SetQuoted records a key whose value LyX expects in quotes.
//
// Windows paths are converted to forward slashes, which is how LyX writes them
// itself. Backslashes inside a quoted LyX string risk being read as escapes.
func (s *Settings) SetQuoted(key, value string) *Settings {
	return s.Set(key, `"`+strings.ReplaceAll(value, `\`, `/`)+`"`)
}

// SetColor overrides one editor colour. The colour name is part of the key's
// identity: \set_color "green" "#b5bd68".
func (s *Settings) SetColor(name, hex string) *Settings {
	s.colors[name] = hex
	return s
}

// Keys reports the managed key names, for tests and the doctor.
func (s *Settings) Keys() []string { return append([]string(nil), s.keys...) }

// Colors reports the managed colour names.
func (s *Settings) Colors() []string {
	out := make([]string, 0, len(s.colors))
	for k := range s.colors {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Len reports how many assignments will be written.
func (s *Settings) Len() int { return len(s.keys) + len(s.colors) }

// Render produces the block that gets written into the preferences file.
func (s *Settings) Render() []string {
	out := []string{blockStart}
	for _, k := range s.keys {
		out = append(out, `\`+k+" "+s.values[k])
	}
	for _, name := range s.Colors() {
		out = append(out, fmt.Sprintf(`\set_color "%s" "%s"`, name, s.colors[name]))
	}
	return append(out, blockEnd)
}

// Apply rewrites the preferences file.
//
// Any previous MadLyX block is removed, along with stray copies of the keys we
// manage wherever they appear, and a fresh block is appended. Settings the user
// chose themselves are left untouched. Running this repeatedly produces an
// identical file - the PowerShell original opened the file in append mode, so a
// second run duplicated every key it wrote.
func Apply(prefPath string, s *Settings) (backup string, err error) {
	existing, err := readLines(prefPath)
	if err != nil {
		return "", err
	}
	if len(existing) > 0 {
		if backup, err = BackupFile(prefPath); err != nil {
			return "", err
		}
	}

	managed := make(map[string]*regexp.Regexp, len(s.keys))
	for _, k := range s.keys {
		managed[k] = regexp.MustCompile(`^\\` + regexp.QuoteMeta(k) + `(\s|$)`)
	}
	colorRes := make([]*regexp.Regexp, 0, len(s.colors))
	for _, name := range s.Colors() {
		colorRes = append(colorRes, regexp.MustCompile(`^\\set_color\s+"`+regexp.QuoteMeta(name)+`"`))
	}

	var kept []string
	inBlock := false
	for _, line := range existing {
		trimmed := strings.TrimSpace(line)
		if trimmed == blockStart {
			inBlock = true
			continue
		}
		if trimmed == blockEnd {
			inBlock = false
			continue
		}
		if inBlock {
			continue
		}
		if matchesAny(trimmed, managed, colorRes) {
			continue
		}
		kept = append(kept, line)
	}

	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	out := append(kept, "")
	out = append(out, s.Render()...)

	if err := os.MkdirAll(filepath.Dir(prefPath), 0o755); err != nil {
		return backup, err
	}
	// LyX reads preferences as UTF-8. No byte-order mark.
	return backup, os.WriteFile(prefPath, []byte(strings.Join(out, "\n")+"\n"), 0o644)
}

func matchesAny(line string, managed map[string]*regexp.Regexp, colors []*regexp.Regexp) bool {
	if line == "" {
		return false
	}
	for _, re := range managed {
		if re.MatchString(line) {
			return true
		}
	}
	for _, re := range colors {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

// IsApplied reports whether our block is present and every managed key is set.
// This is the doctor's check and the step engine's Check, so they cannot
// disagree about what "configured" means.
func IsApplied(prefPath string, s *Settings) (bool, error) {
	lines, err := readLines(prefPath)
	if err != nil || len(lines) == 0 {
		return false, err
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, blockStart) {
		return false, nil
	}
	for _, k := range s.keys {
		want := `\` + k + " " + s.values[k]
		if !strings.Contains(joined, want) {
			return false, nil
		}
	}
	for _, name := range s.Colors() {
		if !strings.Contains(joined, fmt.Sprintf(`\set_color "%s" "%s"`, name, s.colors[name])) {
			return false, nil
		}
	}
	return true, nil
}

// BackupFile copies a file aside before it is modified.
//
// The timestamp has one-second resolution, so two runs in the same second
// would collide. A backup must never overwrite another backup.
func BackupFile(path string) (string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	dir := filepath.Dir(path)
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	ext := filepath.Ext(path)
	stamp := time.Now().Format("20060102-150405")

	dest := filepath.Join(dir, fmt.Sprintf("%s.madlyx-backup-%s%s", base, stamp, ext))
	for n := 2; ; n++ {
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			break
		}
		dest = filepath.Join(dir, fmt.Sprintf("%s.madlyx-backup-%s-%d%s", base, stamp, n, ext))
	}
	return dest, os.WriteFile(dest, src, 0o644)
}

// LatestBackup finds the most recent backup of a file, for rollback.
func LatestBackup(path string) (string, bool) {
	dir := filepath.Dir(path)
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var found []string
	prefix := base + ".madlyx-backup-"
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			found = append(found, e.Name())
		}
	}
	if len(found) == 0 {
		return "", false
	}
	sort.Strings(found)
	return filepath.Join(dir, found[len(found)-1]), true
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lines = append(lines, strings.TrimRight(sc.Text(), "\r"))
	}
	return lines, sc.Err()
}
