//go:build windows

package steps

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Neur0nz/MadLyXInstaller/go/internal/step"
	"github.com/Neur0nz/MadLyXInstaller/go/internal/winenv"
)

func stringsContains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// culmusInstallerURL is the fix the MadLyX guide links for MiKTeX, which has
// no culmus package of its own (p.21).
const culmusInstallerURL = "http://www.ma.huji.ac.il/~sameti/tex/culmusmiktex0.2.2.exe"

func installCulmus(c *step.Context, t winenv.TeX) error {
	tmp, err := os.CreateTemp("", "culmus-*.exe")
	if err != nil {
		return err
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	c.UI.Detail("downloading the Culmus installer")
	if err := download(ctx, culmusInstallerURL, path); err != nil {
		return fmt.Errorf("downloading Culmus: %w", err)
	}

	c.UI.Detail("running the Culmus installer")
	if err := exec.CommandContext(ctx, path).Run(); err != nil {
		c.Log.Logf("culmus installer reported: %v", err)
	}

	// Register the font maps and refresh the filename database: the p.24 fix,
	// done without opening an editor.
	c.UI.Detail("registering Culmus font maps")
	cfg := filepath.Join(os.Getenv("LOCALAPPDATA"), "MiKTeX", "miktex", "config", "updmap.cfg")
	if err := appendLinesIfMissing(cfg, []string{"Map culmus.map", "Map culmusnkd.map"}); err != nil {
		c.Log.Logf("updmap.cfg: %v", err)
	}
	initexmf := filepath.Join(t.BinDir, "initexmf.exe")
	for _, arg := range []string{"--mkmaps", "--update-fndb"} {
		if err := exec.CommandContext(ctx, initexmf, arg).Run(); err != nil {
			c.Log.Logf("initexmf %s: %v", arg, err)
		}
	}
	return nil
}

func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func appendLinesIfMissing(path string, lines []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	existing, _ := os.ReadFile(path)
	var toAdd []string
	for _, l := range lines {
		if !strings.Contains(string(existing), l) {
			toAdd = append(toAdd, l)
		}
	}
	if len(toAdd) == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.Join(toAdd, "\n") + "\n")
	return err
}

// reconfigureLyX makes LyX rescan the TeX installation.
//
// LyX decides which document classes are usable when it configures, and caches
// that in textclass.lst. If it configured while a package was still missing -
// which is exactly what happens when LyX is installed alongside the TeX
// packages - it keeps saying the class is unavailable afterwards. A real run
// produced:
//
//	Warning: Document class not available
//	The selected document class Hebrew Article requires external files
//	that are not available: article.cls, theorem.sty
//
// even though both files were present by then. Rescanning clears it.
//
// LyX ships its own Python on Windows, so this needs nothing installed.
func reconfigureLyX(c *step.Context, l winenv.LyX) error {
	configure := filepath.Join(l.Root, "Resources", "configure.py")
	python := filepath.Join(l.Root, "Python", "python.exe")
	for _, p := range []string{configure, python} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("LyX's own Python or configure.py is missing (%s)", filepath.Base(p))
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, python, configure, "--binary-dir="+filepath.Join(l.Root, "bin"))
	if dir, ok := step.Get[string](c, keyUserDir); ok {
		cmd.Dir = dir // configure.py writes textclass.lst into the working directory
	}
	hideConsole(cmd)
	out, err := cmd.CombinedOutput()
	c.Log.Logf("lyx reconfigure output: %s", string(out))
	return err
}

// textclassAvailable reports whether LyX currently believes it can use a
// document class. This is the same file LyX consults when opening a document,
// so it is exactly what the warning above is driven by.
func textclassAvailable(userDir, class string) bool {
	b, err := os.ReadFile(filepath.Join(userDir, "textclass.lst"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, `"`+class+`"`) {
			continue
		}
		// Format: "heb-article" "article" "Hebrew Article" "true" "" "Articles"
		return strings.Contains(line, `"true"`)
	}
	return false
}

// startLyXOnce launches LyX so it creates its settings folder, then closes it.
//
// The PowerShell version slept a fixed ten seconds and killed the process,
// which both raced on a cold start and risked killing LyX mid-write. This waits
// for the folder and a settled preferences file instead.
func startLyXOnce(c *step.Context, l winenv.LyX) (string, error) {
	cmd := exec.Command(l.Exe)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()

	dir, ok := winenv.WaitFor(90*time.Second, 750*time.Millisecond, func() (string, bool) {
		d, ok := winenv.FindLyXUserDir(l)
		if !ok {
			return "", false
		}
		// Wait for the folder *and* a preferences file, so LyX is never killed
		// while still writing its initial configuration.
		if _, err := os.Stat(filepath.Join(d, "preferences")); err != nil {
			return "", false
		}
		return d, true
	})
	if !ok {
		// LyX may create the folder without a preferences file on some versions.
		if d, found := winenv.FindLyXUserDir(l); found {
			return d, nil
		}
		return "", fmt.Errorf("LyX did not create its settings folder; open and close LyX once, then re-run")
	}
	time.Sleep(2 * time.Second) // let the write settle before killing
	return dir, nil
}
