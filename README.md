# MadLyX Installer

Sets up LyX for writing Hebrew mathematics on Windows, the way
**"שימוש נכון בליך" (The MadLyX)** by Kali describes — plus the quality-of-life
settings the guide doesn't cover.

One command, no Python, no prerequisites.

```powershell
irm https://raw.githubusercontent.com/Neur0nz/MadLyXInstaller/main/bootstrap.ps1 | iex
```

To pass options, use the scriptblock form — `irm | iex` cannot take arguments:

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/Neur0nz/MadLyXInstaller/main/bootstrap.ps1))) -Doctor
```

Or from a clone:

```bash
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

> **Note:** the one-liner points at `bootstrap.ps1`, not `install.ps1`.
> `install.ps1` dot-sources `lib\` and reads `payload\`, and under `iex` there is
> no script on disk — `$PSScriptRoot` is empty and it fails immediately.
> `bootstrap.ps1` is self-contained: it downloads the repository archive (~69 KB),
> extracts it, clears the mark-of-the-web, and runs `install.ps1` from there.
>
> If you fork this, change the `$Repo` default in `bootstrap.ps1` to your own
> `owner/name` — or pass `-Repo owner/name`. It refuses to run while the
> placeholder is still in place.

---

## What it does

**Installs**
- A TeX distribution — **MiKTeX** by default, because it downloads missing
  packages on demand. That one feature removes the guide's entire "installing
  new packages" chapter (pp. 17–19). Falls back to **TeX Live** when your
  Windows user folder contains Hebrew.
- **LyX**, via winget with Chocolatey as backup.
- The Hebrew and maths LaTeX packages: `babel-hebrew`, `culmus`, `preview`,
  `dvipng`, `mathtools`, `stmaryrd`, `listings` and the rest.
- **Culmus for MiKTeX** — MiKTeX has no `culmus` package, which is the
  `LaTeX Error: File culmus.sty not found` failure on p. 21. Registers the
  `culmus.map` / `culmusnkd.map` font maps too (the p. 24 fix), non-interactively.

**Configures LyX** (all from the guide unless noted)

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
| `\forward_search_pdf` | Ctrl+click jumps to that spot in the PDF | *added* |

**Installs the MadLyX payload**
- The **shortcut file** — ~500 shortcuts, automatically picking the build that
  matches your LyX (2.3 is bind Format 4, 2.4 is Format 5; the wrong one
  silently loses shortcuts).
- The **five document templates**, wired into `Ctrl+Shift+N`.
- The **macro files** (Hebrew and English) into `Documents\MadLyX\macros`.
- A **shared LaTeX preamble** collecting the guide's fixes: muted colours, the
  `\Longrightarrow` repair (pp. 71–72), disjoint union (p. 72), the `listings`
  style (p. 63), the blank-first-page workaround (p. 20), and Hebrew
  justification (*added* — Hebrew has no LaTeX hyphenation, so `\sloppy` and
  `\emergencystretch` prevent overfull lines).

**Offers, asking each time**
- **Disabling the Alt+Shift language switch.** The guide's standing instruction
  is to keep Windows on ENG (p. 17) — every LyX shortcut breaks when Windows
  flips to Hebrew, and Alt+Shift is easy to hit while reaching for Alt shortcuts.
  Sets `HKCU\Keyboard Layout\Toggle` so Alt+Shift no longer switches. Win+Space
  and the taskbar picker still work, so Hebrew stays available deliberately.
  Per-user, no admin, reversible in Windows Settings.
- **SumatraPDF**, for jump-to-source in both directions.
- **Windows Defender exclusions** for the TeX and LyX folders. LaTeX churns
  through many small files per compile and MiKTeX writes thousands when
  installing packages; excluding them is a real speed-up. Needs admin.

**Verifies** by compiling a bundled Hebrew test document to PDF. If it fails,
you get the actual cause and its fix rather than "something went wrong".

---

## Checking and fixing later

```powershell
.\install.ps1 -Doctor
```

Changes nothing. Checks for Hebrew in your profile path, LyX and TeX presence,
every managed preference key, whether your shortcut file matches your LyX
version, whether `culmus.sty` and `cp1255.def` resolve, and whether Alt+Shift is
still live — printing the fix for anything wrong. This covers most of the
guide's nine-page troubleshooting chapter mechanically.

## Options

| Flag | Effect |
|---|---|
| `-Doctor` | Diagnostics only |
| `-Unattended` | Never prompt; system-level changes are **skipped**, not assumed |
| `-TeXDistribution miktex\|texlive\|auto` | Override the distribution choice |
| `-SkipSmokeTest` | Skip the final test compile |
| `-SkipSystemTweaks` | LyX and TeX only; nothing outside them |
| `-NoElevate` | Never offer to restart with administrator rights |

`bootstrap.ps1` accepts all of these and forwards them, plus `-Repo owner/name`,
`-Branch`, and `-KeepFiles` (keeps the downloaded copy instead of deleting it).

## Versions

| | |
|---|---|
| **LyX** | **2.4.4.1** via `winget install LyX.LyX` (Chocolatey as backup) |
| **TeX** | MiKTeX current, or TeX Live `scheme-small` on the Hebrew-path fallback |

If LyX is **already installed**, the installer leaves it alone and adapts to it —
LyX 2.3 gets the Format 4 bind file and no colour overrides; 2.4 gets Format 5
and the muted editor colours. Nothing is upgraded behind your back.

`scheme-small` rather than `scheme-basic` is deliberate: `scheme-basic` omits
`mathtools`, `stmaryrd`, `relsize`, `preview` and `dvipng`, and TeX Live has no
on-demand installation to recover with.

## Forking

1. Change the `$Repo` default in `bootstrap.ps1` to your `owner/name`.
2. Update the two URLs at the top of this README.
3. The repo must be **public** — `codeload.github.com` needs no auth for public
   repos, but a private one would require a token.

`raw.githubusercontent.com` caches for about five minutes, so a freshly pushed
`bootstrap.ps1` may serve a stale copy briefly.

Windows PowerShell 5.1 is handled: `bootstrap.ps1` forces TLS 1.2 (some 5.1
builds still negotiate TLS 1.0, which GitHub rejects) and calls `Unblock-File`
on the extracted scripts so the `RemoteSigned` execution policy does not block
them.

---

## Safe to re-run

Every run backs up `preferences` and `user.bind` before touching them, and
backups never overwrite each other. Settings are written into a marked block:

```
### BEGIN MadLyXInstaller - do not edit inside this block
...
### END MadLyXInstaller
```

Re-running **replaces** that block and strips stray copies of the keys it
manages. Settings you set yourself are left alone. Run it ten times and the file
looks identical — verified by `tests\Test-Configure.ps1`.

Backups are `preferences.madlyx-backup-<timestamp>` next to the original.

## Tests

```powershell
.\tests\Test-Configure.ps1
```

26 assertions covering idempotency, preservation of unmanaged settings, path
normalisation, encoding, LyX-version-correct bind selection and template
installation. Runs in a sandbox; needs neither LyX nor TeX installed.

---

## After installing

- **F12** switches between Hebrew and English *inside LyX*.
- **Keep Windows itself on ENG.** LyX supplies the Hebrew.
- **Ctrl+Shift+N** starts a document from a MadLyX template.
- **Check the shortcuts loaded**: in a maths box, `Alt+W` then `A` gives α.
- **Keep Hebrew out of folder names** anywhere above your documents.

To use the macros: `Insert > File > Child Document`, pick the file from
`Documents\MadLyX\macros`, and set the include type to **Input**. Set the
resulting box to `Standard` layout, not a heading, or you get a blank first
page (p. 41).

---

## Known limitations

- **Windows only.** The guide points macOS users to
  [Sraya's guide](https://srayaa.wixsite.com/math/various); a Mac bind file
  exists (`bit.ly/MacOsUserBind`) but this installer does not target macOS.
- **`hyperref` is left commented out** in the shared preamble. It gives
  clickable cross-references and PDF bookmarks, but the guide reports it
  throwing errors in the `heb-article` class (p. 53). Enable it per-document in
  the Standard/AMS templates.
- **Inverse search** (PDF → LyX) is configured *inside SumatraPDF*, not LyX, so
  the installer prints the exact command to paste rather than setting it.
- **Hebrew usernames cannot be fixed.** Windows has no supported way to rename a
  user profile folder. The installer switches to TeX Live, which copes better,
  and warns. The real fix is a new account with an English name.
- **`kalikton.py` / `kalilmod.py` are not included** — they need Python, which
  this installer deliberately avoids. If you want them, get them from the guide.
  The better-engineered approach for that kind of task is driving LyX itself via
  `lyx -x "<LFUN>"` ([LyX wiki: LyxFunctions](https://wiki.lyx.org/LyX/LyxFunctions))
  rather than text-munging `.lyx` files.
- **LuaLaTeX / fontspec is not supported.** The modern Hebrew stack
  (`polyglossia` + `bidi` + real OpenType fonts) gives better typography and no
  `cp1255` encoding errors, but it is slower, the MadLyX templates and macros
  don't port to it, and everyone else in your class is on pdflatex. Classic
  stack by default is deliberate.

---

## Credits

The entire setup, the shortcut file, the macros and the templates are
**Kali's** work, from *The MadLyX*. This repository is an installer for it.
See [CREDITS.md](CREDITS.md).
