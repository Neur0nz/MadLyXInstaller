# MadLyX Installer

Sets up **LyX** on Windows for writing mathematics in Hebrew — everything
configured the way **"שימוש נכון בליך" (The MadLyX)** by Kali describes, in one
command, in about four minutes.

You do not need to install anything first. No Python, no LaTeX, no setup.

```powershell
irm https://raw.githubusercontent.com/Neur0nz/MadLyXInstaller/main/bootstrap.ps1 | iex
```

Open PowerShell, paste that, press Enter. It will not ask for administrator
permission.

---

## ⚠️ Vibecoded
most of this project was written for fun and vibecoded with Claude, while I have tested things and am pretty confident, take that into account.

What that means in practice:

- **It has been tested on real machines**, repeatedly, from a completely clean
  Windows install through to a Hebrew PDF coming out the other end. The timings
  and behaviour described here are measured, not guessed.
- **The code is open.** Anything it does to your computer, you can read in this
  repository first.

If that trade sounds fine to you, it will save you a long, fiddly afternoon. If
it does not, the guide tells you how to do all of this by hand.

---

## What you get

**A working LaTeX setup**
MiKTeX (the TeX engine) and LyX itself, installed for your user account only —
which is why Windows never asks for permission. Missing LaTeX packages install
themselves later, automatically, so you never hit "package not found".

**Hebrew that actually works**
The Culmus fonts, the `babel-hebrew` support files, and the font registration
the guide spends two pages explaining. Press **F12** in LyX to switch between
Hebrew and English while typing.

**About 500 maths shortcuts**
Kali's shortcut file, matched to your LyX version. In a maths box, `Alt+W` then
`A` gives you α. No more hunting through menus for symbols.

**Five ready-made documents**
Press **Ctrl+Shift+N** in LyX and start from a template that is already set up
for Hebrew — homework, articles, two-column, English.

**Kali's macros and a shared preamble**
Dropped into `Documents\MadLyX`, with the guide's LaTeX fixes already applied
and about twenty optional extras you can switch on by uncommenting a line.

**Sensible settings**
Instant maths preview, autosave, crash backups, readable editor colours, arrow
keys that behave correctly in right-to-left text. Every setting checked against
LyX's own source rather than copied from a forum post.

**It proves it worked**
Before finishing, it compiles a Hebrew document to PDF. If that fails, it tells
you which of the guide's known problems you hit, rather than "something went
wrong".

---

## Before you start

- **Windows 10 or 11.** macOS users: see
  [Sraya's guide](https://srayaa.wixsite.com/math/various).
- **About 2 GB of disk space** and a few minutes.
- **Keep Hebrew out of your folder names.** If your Windows username is in
  Hebrew, PDF export breaks — this is the single most common cause of failure in
  the guide, and Windows gives no supported way to rename a profile folder. The
  installer detects it, warns you, and switches to a TeX distribution that copes
  better, but the real fix is an account with an English name.

## If Windows blocks it

Some Windows 11 machines run **Smart App Control**, which refuses programs it
has not seen before. If you get *"An Application Control policy has blocked this
file"*, that is what happened — the installer is not signed with a paid
certificate.

It is not a virus warning and nothing is wrong with your computer. Tell whoever
sent you this link; a rebuild usually clears it. Do **not** turn Smart App
Control off to work around this — once disabled, Windows will not let you turn
it back on without reinstalling.

---

## After it finishes

- **F12** switches between Hebrew and English inside LyX.
- **Keep Windows itself on English** — LyX supplies the Hebrew.
- **Ctrl+Shift+N** starts a document from a MadLyX template.
- Check the shortcuts loaded: in a maths box, **Alt+W** then **A** gives α.

## Commands

| Command | What it does |
|---|---|
| *(no argument)* | Install and configure everything |
| `doctor` | Check your setup and report problems. Changes nothing |
| `uninstall` | Undo the changes this installer made, restoring your backups |
| `--dry-run` | Show what would change, without changing it |
| `--elevate` | Also add Windows Defender exclusions, which make compiling faster. This is the only part that needs administrator rights, so it asks |

To pass a command, use this form instead — `irm | iex` cannot take arguments:

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/Neur0nz/MadLyXInstaller/main/bootstrap.ps1))) doctor
```

## Safe to run twice

Every step checks whether its work is already done, so re-running skips whatever
is finished. If it stops halfway — lost connection, closed window — just run it
again and it carries on.

Already have LyX? It uses the one you have and only adds the MadLyX
configuration. Your existing preferences are backed up first, and only the
settings MadLyX manages are touched; anything you chose yourself is left alone.
Your settings live inside a marked block:

```
### BEGIN MadLyXInstaller - do not edit inside this block
...
### END MadLyXInstaller
```

One thing to know: if you have customised your own LyX keyboard shortcuts, the
MadLyX shortcut file **replaces** them. The old one is backed up beside it, and
`uninstall` puts it back.

---

## Credits

The setup, the shortcut file, the macros and the templates are all **Kali's**
work, from *The MadLyX*. This repository only installs them.
See [CREDITS.md](CREDITS.md).

## Known limitations

- **Windows only.**
- **Hebrew usernames cannot be fixed** — see above.
- **TeX Live must be installed manually.** MiKTeX is the supported automatic
  path.
- **`kalikton.py` / `kalilmod.py` are not included.** They need Python, which
  this installer deliberately avoids.
- **LuaLaTeX / fontspec is not supported.** Better typography, but slower, and
  Kali's templates and macros do not port to it.

## For developers

Written in Go, shipped as a single executable with everything embedded, so
there is nothing to install first. The installer is a table of steps, each with
a `Check` that asks the machine what is already true and an `Apply` that does
the work — which is what makes the dry run, the resume-after-failure and
`doctor` all fall out of the same definitions.

```
bootstrap.ps1              the one-liner: downloads the release and runs it
payload/                   Kali's files, shipped byte-for-byte unmodified
go/cmd/madlyx              command line
go/internal/steps          what the installer does, as data
go/internal/step           the engine: checks, apply, rollback, scheduling
go/internal/ui             the only code that writes to the terminal
```

Build it yourself:

```bash
cp -r payload go/internal/payload/data
cd go && go build -o madlyx.exe ./cmd/madlyx
```

Releases are built by GitHub Actions and carry
[build provenance](https://github.com/Neur0nz/MadLyXInstaller/attestations):

```bash
gh attestation verify madlyx.exe --repo Neur0nz/MadLyXInstaller
```
