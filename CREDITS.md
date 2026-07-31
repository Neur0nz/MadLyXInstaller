# Credits

## The MadLyX

Everything this installer sets up comes from **"שימוש נכון בליך"** — *The
MadLyX* — written and maintained by **Kali** (Michael Kali).

- Latest version of the guide: <https://mkali56.wixsite.com/madlyx>
- LyX support group ("ליך - טריקים ושטיקים"):
  <https://chat.whatsapp.com/5nFleCW9dPoLYosDgTYEXU>

The guide is the reason this project exists. It is worth reading in full, and
the second half — on *using* LyX rather than installing it — is not covered by
any installer.

## Bundled files

These are redistributed as-is from the links published in the guide. All
authored by Kali unless noted.

| File in this repo | Original |
|---|---|
| `payload/bind/madlyx-2.4.bind` | <https://bit.ly/KaliLyxUserBind2p4> |
| `payload/bind/madlyx-2.3.bind` | <https://bit.ly/KaliLyxUserBind> |
| `payload/macros/madlyx-macros-he.lyx` | Google Drive link in the guide (p. 86) |
| `payload/macros/madlyx-macros-en.lyx` | Google Drive link in the guide (p. 86) |
| `payload/templates/01-standard-minimal.lyx` | <http://bit.ly/3DhYe5T> |
| `payload/templates/02-hebrew-article.lyx` | <https://bit.ly/3SPiFNf> |
| `payload/templates/03-standard-fancy.lyx` | Google Drive link in the guide (p. 87) |
| `payload/templates/04-two-column.lyx` | Google Drive link in the guide (p. 87) |
| `payload/templates/05-english.lyx` | Google Drive link in the guide (p. 87) |

They are vendored rather than downloaded at install time so the installer keeps
working if a `bit.ly` redirect or Drive link rots or starts rate-limiting.

**If Kali would prefer these not be redistributed, open an issue and they will
be replaced with download-at-install-time.**

## Contributors credited within the guide

The guide credits, among others: **Or Yaar** (MiKTeX font fixes, pp. 23–24),
**Amit Tsarfati** (blank first page, p. 20), **Yitzhak Cohen** (title-frame
spacing, p. 21), **Gilad Sharam** (styled theorem environments, p. 49; text
insertion shortcut, p. 37), **Ziv Rosenwasser** (`mathescape` in listings,
p. 63), **Neve Moitzman** (macOS shortcut file, p. 76), **Yoav Aloni** (the Zoom
Alt+M conflict, p. 34).

## Other projects referenced

- **Arazim project** — the original automatic installer this one supersedes:
  <https://arazim-project.com/lyx-hebrew/>
- **Culmus** — the Hebrew font family and its LaTeX package. The
  MiKTeX installer is hosted by HUJI:
  <http://www.ma.huji.ac.il/~sameti/tex/culmusmiktex0.2.2.exe>
- **Sraya's macOS guide**: <https://srayaa.wixsite.com/math/various>
- **LyX**: <https://www.lyx.org>
- **MiKTeX**: <https://miktex.org>, **TeX Live**: <https://tug.org/texlive/>
- **SumatraPDF**: <https://www.sumatrapdfreader.org>

## This installer

The Go source under `go/`, the `bootstrap.ps1` launcher, and the assembled
`payload/preamble/madlyx-preamble.tex` are the contribution of this repository.
The preamble's contents are taken from the guide, with page references in the
comments; the Hebrew justification block is the only addition.
