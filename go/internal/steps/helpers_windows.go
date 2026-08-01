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

	"github.com/Neur0nz/MadLyXInstaller/go/internal/payload"
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

// hebrewDictURLs are the Hunspell files LyX wants for Hebrew spell checking.
//
// LyX's own installer fetches these from
// https://www.lyx.org/trac/export/HEAD/lyxsvn/dictionaries/trunk/dicts/, which
// now returns 404 for both - verified. The LibreOffice dictionary repository
// carries the same files and serves them.
var hebrewDictURLs = map[string]string{
	"he_IL.aff": "https://raw.githubusercontent.com/LibreOffice/dictionaries/master/he_IL/he_IL.aff",
	"he_IL.dic": "https://raw.githubusercontent.com/LibreOffice/dictionaries/master/he_IL/he_IL.dic",
}

// placeHebrewDictionary puts the Hunspell files where LyX's installer expects
// them, before that installer runs.
//
// On a machine whose Windows language is Hebrew, LyX's installer selects the
// Hebrew dictionary automatically and downloads it. That download 404s, and the
// installer reports it with MessageBox - which NSIS still displays under /S. So
// a silent install stops dead on two modal dialogs, in Hebrew, waiting for
// someone to click. A student reported exactly that.
//
// The installer skips the download when the file is already present:
//
//	${IfNot} ${FileExists} "$INSTDIR\Resources\dicts\$R9"
//	  inetc::get ...
//
// So writing the files first removes the dialogs and, unlike simply suppressing
// them, leaves Hebrew spell checking actually working.
//
// Failure here is not fatal: the worst case is the dialogs LyX would have shown
// anyway.
//
// It is called twice, with the two directories that can be right. Before the
// install, with the one the per-user path is about to use - predicted, because
// the files have to exist before the installer looks. After the install, with
// the directory LyX actually landed in, which closes the case where the
// per-user attempt failed and the machine-wide fallback put LyX somewhere else:
// the prediction would have seeded an empty folder and the real installation
// would have no dictionary. The second call costs nothing when the prediction
// was right, because files already present are skipped.
func placeHebrewDictionary(c *step.Context, lyxRoot string) {
	if lyxRoot == "" {
		return
	}
	dir := filepath.Join(lyxRoot, "Resources", "dicts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		// Program Files is not writable without administrator rights, which is
		// expected on the machine-wide fallback; nothing here is worth failing
		// the install over.
		c.Log.Logf("hebrew dictionary (%s): %v", dir, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	for name, url := range hebrewDictURLs {
		dest := filepath.Join(dir, name)
		if st, err := os.Stat(dest); err == nil && st.Size() > 0 {
			continue
		}
		if err := download(ctx, url, dest); err != nil {
			c.Log.Logf("hebrew dictionary %s: %v", name, err)
			// Leave nothing half-written: a truncated file would satisfy the
			// installer's existence check and break spell checking silently.
			os.Remove(dest)
			continue
		}
		c.Log.Logf("placed %s", dest)
	}
}

// predictedLyXRoot is where the per-user install is about to put LyX.
//
// The dictionary has to be in place before the installer runs, so this is the
// one thing that cannot wait to be discovered. It mirrors the installer's own
// default: MULTIUSER_INSTALLMODE_INSTDIR is "LyX <major>.<minor>", under
// %LOCALAPPDATA%\Programs in /CurrentUser mode.
func predictedLyXRoot(installerName string) string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs",
		"LyX "+lyxSeriesFromInstaller(installerName))
}

// lyxSeriesFromInstaller reads "2.4" out of "LyX_2.4.4.1_X64_nullsoft_en-US.exe".
//
// The installer's own default directory is "LyX <major>.<minor>", so this has
// to track the file rather than be hard-coded, or it will silently stop working
// when LyX 2.5 ships.
func lyxSeriesFromInstaller(name string) string {
	var major, minor int
	if n, _ := fmt.Sscanf(name, "LyX_%d.%d", &major, &minor); n == 2 {
		return fmt.Sprintf("%d.%d", major, minor)
	}
	return "2.4"
}

// warmCompile runs one throwaway LaTeX compile so that MiKTeX builds its
// formats, font metrics and caches now rather than during the LyX install.
//
// This is the third attempt at the same nine minutes, and the first two were
// wrong, so here is what the evidence actually says. During the LyX install
// MiKTeX logs roughly 350 operations a minute for the whole nine minutes, and
// miktex-fc-cache is invoked 129 times, once every three seconds. LyX's own
// installer calls initexmf twice, not 129 times, so those invocations come
// from inside MiKTeX generating things on demand.
//
// Calling miktex-fc-cache up front did not help: it was still invoked 130
// times afterwards. The one measurement where the same installer finished in
// 35 seconds was against a MiKTeX that had already compiled a document - which
// is the difference. Formats and font metrics are built by compiling, not by
// refreshing a cache.
//
// So compile something. The bundled Hebrew test document is the right choice:
// it pulls in the same fonts and packages the real work needs, so nothing is
// warmed that would not have been needed anyway.
func warmCompile(c *step.Context, t winenv.TeX) {
	work, err := os.MkdirTemp("", "madlyx-warm")
	if err != nil {
		return
	}
	defer os.RemoveAll(work)

	src := filepath.Join(work, "warm.tex")
	if err := payload.WriteTo("data/smoketest/smoketest.tex", src); err != nil {
		c.Log.Logf("warm compile: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, filepath.Join(t.BinDir, "pdflatex.exe"),
		"-interaction=nonstopmode", "warm.tex")
	cmd.Dir = work
	hideConsole(cmd)
	out, err := cmd.CombinedOutput()
	c.Log.Logf("warm compile finished (err=%v), output: %s", err, string(out))
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
