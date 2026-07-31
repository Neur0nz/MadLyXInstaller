# MadLyX Installer

Sets up LyX for writing Hebrew mathematics on Windows, the way
**"שימוש נכון בליך" (The MadLyX)** by Kali describes — plus the quality-of-life
settings the guide doesn't cover.

One command. No Python, no prerequisites, nothing to install first.

```powershell
irm https://raw.githubusercontent.com/Neur0nz/MadLyXInstaller/main/bootstrap.ps1 | iex
```

To pass options, use the scriptblock form — `irm | iex` cannot take arguments:

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/Neur0nz/MadLyXInstaller/main/bootstrap.ps1))) doctor
```

Or download `madlyx.exe` from [Releases](https://github.com/Neur0nz/MadLyXInstaller/releases)
and run it directly.

---

## What it does

**Installs** MiKTeX (chosen because it downloads missing packages on demand,
which removes the guide's entire "installing new packages" chapter, pp. 17–19),
LyX, the Hebrew and maths LaTeX packages, and Culmus support including the
MiKTeX font-map registration that the guide spends two pages on (pp. 23–24).

**Configures LyX** — every key verified against LyX's own `LyXRC.cpp` tag table
rather than recalled:

| Setting | Why | Source |
|---|---|---|
| `\gui_language english` | Every guide and forum answer is in English; menu keyboard navigation needs it | p. 14 |
| `\kbmap` + `null` / `hebrew` | Hebrew comes from LyX, not Windows | fig. 0.4 |
| `\visual_cursor true` | Arrow keys behave correctly in RTL text | fig. 0.3 |
| `\scroll_below_document true` | Scroll past the end of the document | fig. 0.5 |
| `\bind_file "cua"` | Keeps Ctrl+S / Ctrl+M working alongside the MadLyX shortcuts | pp. 75–77 |
| `\path_prefix` | Lets LyX find the TeX binaries | — |
| `\preview on` | Renders maths and images inline in the editor | *added* |
| `\autosave` + `\make_backup` | Crash protection | *added* |
| `\set_color` × 6 | Muted editor colours; 2.4's defaults are unreadable | p. 46 |

**Installs the MadLyX payload** — the ~500-shortcut bind file matched to your
LyX version, the five templates (wired into `Ctrl+Shift+N`), the macro files,
and a shared preamble collecting the guide's LaTeX fixes.

**Offers, asking first** — Defender exclusions for the TeX tree, which speed up
compiling noticeably.

> The installer does **not** touch your Windows keyboard shortcuts. The guide
> advises keeping Windows on ENG *while typing in LyX* (p. 17), since LyX
> supplies the Hebrew itself via F12 — but Alt+Shift is how you switch language
> everywhere else, and disabling it system-wide would be a bad trade.

**Verifies** by compiling a bundled Hebrew document to PDF. On failure it maps
the error onto the guide's documented fix rather than saying "something went
wrong".

---

## Commands

```
madlyx                 install and configure everything
madlyx doctor          check the installation; changes nothing
madlyx uninstall       restore settings from backup, undo system changes
```

| Flag | Effect |
|---|---|
| `--dry-run` | Report what would change, change nothing |
| `--yes` / `-y` | Never ask; system changes are **skipped**, not assumed |
| `--tex miktex\|texlive\|auto` | Override the distribution choice |
| `--skip-test` | Skip the final Hebrew test compile |
| `--skip-system` | LyX and TeX only; nothing outside them |
| `--no-elevate` | Never offer to restart with administrator rights |

## Safe to re-run

Each step reports whether its work is already done, so a second run applies only
what's outstanding. If a run is interrupted, running it again **resumes** rather
than starting over.

Settings live in a marked block:

```
### BEGIN MadLyXInstaller - do not edit inside this block
...
### END MadLyXInstaller
```

Re-running replaces that block and strips stray copies of the keys it manages.
Settings you chose yourself are left alone. `preferences` and `user.bind` are
backed up before any change, and a backup never overwrites another backup.

## Versions

| | |
|---|---|
| **LyX** | 2.4.4.1 via winget, Chocolatey as fallback |
| **TeX** | MiKTeX, with TeX Live when the profile path contains Hebrew |

**If LyX is already installed it is left alone** and the installer adapts:
2.3 gets the Format 4 bind file and no colour overrides; 2.4 and later get
Format 5 and muted colours. LyX 2.5 has no dedicated MadLyX build, so it gets
the 2.4 file — safe, because LyX converts a mismatched bind format on load
(`KeyMap::read` in the LyX source). An existing TeX distribution is always
reused; a second one is never installed.

---

## After installing

- **F12** switches between Hebrew and English *inside LyX*
- **Keep Windows itself on ENG** — LyX supplies the Hebrew
- **Ctrl+Shift+N** starts a document from a MadLyX template
- **Check the shortcuts loaded**: in a maths box, `Alt+W` then `A` gives α
- **Keep Hebrew out of folder names** anywhere above your documents

To use the macros: `Insert > File > Child Document`, pick a file from
`Documents\MadLyX\macros`, set the include type to **Input**, and set the
resulting box to `Standard` layout — not a heading, or you get a blank first
page (p. 41).

---

## Building

```bash
cp -r payload go/internal/payload/data   # go:embed cannot reach outside its package
cd go && go test ./... && go build -o madlyx.exe ./cmd/madlyx
```

Go is a **build-time dependency only**. The released binary imports nothing but
Windows system DLLs and runs on a machine with an empty `PATH`.

### Layout

| | |
|---|---|
| `internal/ui` | The only code that prints. Detects the terminal once and degrades to plain lines when piped |
| `internal/step` | The engine. Steps are data with a `Check`/`Apply`/`Undo`; dry-run, resume, rollback and the doctor all derive from that |
| `internal/steps` | What the installer actually does |
| `internal/lyxcfg` | Idempotent preferences writer, collision-proof backups |
| `internal/winenv` | LyX and TeX discovery |
| `internal/winsys` | Registry, Defender, elevation |
| `internal/pkgmgr` | winget / Chocolatey / mpm behind a testable interface |
| `internal/payload` | Kali's files, compiled in |

`doctor` is `Plan.Diagnose` over the same `Check` functions the installer uses,
so the two cannot drift about what "configured" means.

### Verifying a release

```bash
gh attestation verify madlyx.exe --repo Neur0nz/MadLyXInstaller
```

Releases carry [SLSA build provenance](https://docs.github.com/en/actions/concepts/security/artifact-attestations),
so the binary is verifiably built by the release workflow from a specific commit.

---

## Known limitations

- **Windows only.** The guide points macOS users to
  [Sraya's guide](https://srayaa.wixsite.com/math/various).
- **`hyperref` is left commented out** in the shared preamble — it gives
  clickable cross-references, but the guide reports it erroring in the
  `heb-article` class (p. 53). Enable it per-document in the Standard templates.
- **Hebrew usernames cannot be fixed.** Windows has no supported way to rename a
  user profile folder. The installer switches to TeX Live, which copes better,
  and warns. The real fix is an account with an English name.
- **TeX Live must be installed manually** — MiKTeX is the supported automatic
  path.
- **`kalikton.py` / `kalilmod.py` are not included**; they need Python, which
  this installer deliberately avoids. The better-engineered approach for that
  kind of task is driving LyX itself via `lyx -x "<LFUN>"`
  ([LyX wiki](https://wiki.lyx.org/LyX/LyxFunctions)).
- **LuaLaTeX / fontspec is not supported.** Better typography, but slower, and
  Kali's templates and macros do not port to it.

## Credits

The entire setup, the shortcut file, the macros and the templates are **Kali's**
work, from *The MadLyX*. This repository is an installer for it.
See [CREDITS.md](CREDITS.md).
