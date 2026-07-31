# Test-ExistingLyX.ps1
#
# Covers the "user already has LyX" path across every version series, including
# ones that do not exist yet. Runs entirely in a sandbox: no LyX, no TeX needed.
#
# This file is deliberately pure ASCII. Windows PowerShell 5.1 reads a .ps1 with
# no byte-order mark using the system ANSI codepage, so literal Hebrew here would
# be corrupted before Test-HasHebrew ever saw it, and these assertions would be
# testing a mangled string.

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repo = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
foreach ($m in 'Common','Preflight','LyX','TeX','Configure','SystemTweaks','Doctor') { . "$repo\lib\$m.ps1" }

$script:MadLyxUnattended = $true
$sandbox = Join-Path $env:TEMP "madlyx-existing-$(Get-Random)"
New-Item -ItemType Directory -Force $sandbox | Out-Null
Initialize-MadLyxLog (Join-Path $sandbox 'test.log')

$fails = 0
function Check($label, $condition) {
    if ($condition) { Write-Host "  PASS  $label" -ForegroundColor Green; return 0 }
    Write-Host "  FAIL  $label" -ForegroundColor Red; return 1
}

function New-FakeLyX([string]$ver) {
    $parts = $ver.Split('.')
    $series = "$($parts[0]).$($parts[1])"
    return [pscustomobject]@{
        Root = "C:\Program Files\LyX $series"; Exe = "C:\Program Files\LyX $series\bin\LyX.exe"
        Version = [version]$ver; Series = $series; UserDirName = "LyX$series"
    }
}

function New-HebrewString([int[]]$Codes) {
    return (-join ($Codes | ForEach-Object { [char]$_ }))
}

$fakeTex = [pscustomobject]@{ Distro='miktex'; BinDir='C:\Program Files\MiKTeX\miktex\bin\x64' }

# ---------------------------------------------------------------------------
#  Bind file selection across versions, including future ones
# ---------------------------------------------------------------------------
Write-Host "`n=== bind file selection by LyX version ===" -ForegroundColor Cyan
$cases = @(
    @{ Ver='2.2.4';  Series='2.3'; Exact=$false; Format='Format 4' }
    @{ Ver='2.3.7';  Series='2.3'; Exact=$true;  Format='Format 4' }
    @{ Ver='2.4.4';  Series='2.4'; Exact=$true;  Format='Format 5' }
    @{ Ver='2.5.1';  Series='2.4'; Exact=$false; Format='Format 5' }   # current stable
    @{ Ver='2.10.0'; Series='2.4'; Exact=$false; Format='Format 5' }   # string-compare trap
    @{ Ver='3.0.0';  Series='2.4'; Exact=$false; Format='Format 5' }
)
foreach ($c in $cases) {
    $lyx = New-FakeLyX $c.Ver
    $choice = Get-MadLyxBindSeries -LyX $lyx
    $fails += Check "LyX $($c.Ver) -> bind $($c.Series) (got $($choice.Series))" ($choice.Series -eq $c.Series)
    $fails += Check "LyX $($c.Ver) exact-match flag = $($c.Exact)" ($choice.Exact -eq $c.Exact)

    $userDir = Join-Path $sandbox "u$($c.Ver -replace '\.','')"
    New-Item -ItemType Directory -Force $userDir | Out-Null
    Install-MadLyxBindFile -UserDir $userDir -LyX $lyx -PayloadRoot "$repo\payload" *>$null
    $hdr = @(Get-Content (Join-Path $userDir 'bind\user.bind') -TotalCount 5 | Where-Object { $_ -match '^Format' })
    $fails += Check "LyX $($c.Ver) installed $($c.Format)" (($hdr -join '') -match [regex]::Escape($c.Format))
}

# ---------------------------------------------------------------------------
#  Editor colours: 2.4+ only
# ---------------------------------------------------------------------------
Write-Host "`n=== editor colour overrides ===" -ForegroundColor Cyan
$fails += Check '2.3.7 gets no colours'  ((Get-MadLyxEditorColors -LyX (New-FakeLyX '2.3.7')).Count  -eq 0)
$fails += Check '2.4.4 gets colours'     ((Get-MadLyxEditorColors -LyX (New-FakeLyX '2.4.4')).Count  -gt 0)
$fails += Check '2.5.1 gets colours'     ((Get-MadLyxEditorColors -LyX (New-FakeLyX '2.5.1')).Count  -gt 0)
$fails += Check '2.10.0 gets colours'    ((Get-MadLyxEditorColors -LyX (New-FakeLyX '2.10.0')).Count -gt 0)

# ---------------------------------------------------------------------------
#  Existing config is preserved, not clobbered
# ---------------------------------------------------------------------------
Write-Host "`n=== a user who already had LyX configured ===" -ForegroundColor Cyan
$lyx = New-FakeLyX '2.4.4'
$userDir = Join-Path $sandbox 'established'
New-Item -ItemType Directory -Force (Join-Path $userDir 'bind') | Out-Null

# Their own hand-tuned settings and their own shortcuts.
@'
Format 36
\user_name "Existing Student"
\user_email "student@example.ac.il"
\screen_zoom 130
\autosave 60
\kbmap false
'@ | Set-Content (Join-Path $userDir 'preferences') -Encoding UTF8
@'
Format 5
\bind "C-M-x" "my-own-personal-shortcut"
'@ | Set-Content (Join-Path $userDir 'bind\user.bind') -Encoding UTF8

$s = Get-MadLyxSettings -Tex $fakeTex -LyX $lyx -UserDir $userDir
Set-LyXPreferences -UserDir $userDir -Settings $s -Colors (Get-MadLyxEditorColors -LyX $lyx) *>$null
Install-MadLyxBindFile -UserDir $userDir -LyX $lyx -PayloadRoot "$repo\payload" *>$null

$prefs = Get-Content (Join-Path $userDir 'preferences')
$fails += Check 'their \user_name survived'   ([bool]($prefs -match 'Existing Student'))
$fails += Check 'their \user_email survived'  ([bool]($prefs -match 'student@example.ac.il'))
$fails += Check 'their \screen_zoom survived' ([bool]($prefs -match '^\\screen_zoom 130'))
$fails += Check 'their \autosave 60 was overridden to 300' ([bool]($prefs -match '^\\autosave 300'))
$fails += Check 'their \kbmap false was corrected to true' ([bool]($prefs -match '^\\kbmap true'))
$fails += Check 'only one \autosave line' (@($prefs | Where-Object { $_ -match '^\\autosave' }).Count -eq 1)

$backups = @(Get-ChildItem $userDir -Filter 'preferences.madlyx-backup*')
$fails += Check 'their original preferences was backed up' ($backups.Count -eq 1)
$fails += Check 'the backup still holds their original \autosave 60' `
    ([bool]((Get-Content $backups[0].FullName) -match '^\\autosave 60'))

$bindBackups = @(Get-ChildItem (Join-Path $userDir 'bind') -Filter 'user.madlyx-backup*')
$fails += Check 'their own shortcut file was backed up' ($bindBackups.Count -eq 1)
$fails += Check 'the backup still holds their personal bind' `
    ([bool]((Get-Content $bindBackups[0].FullName) -match 'my-own-personal-shortcut'))

# ---------------------------------------------------------------------------
#  Get-TeXPlan never installs a second distribution
# ---------------------------------------------------------------------------
Write-Host "`n=== existing TeX distribution is reused ===" -ForegroundColor Cyan
$pf = [pscustomobject]@{ ExistingMiKTeX=$fakeTex; ExistingTeXLive=$null; RecommendTeXLive=$false }
$plan = Get-TeXPlan -Preflight $pf -Requested 'auto'
$fails += Check 'existing MiKTeX is reused' ($plan.Action -eq 'use-existing' -and $plan.Distro -eq 'miktex')

$tl = [pscustomobject]@{ Distro='texlive'; BinDir='C:\texlive\2025\bin\windows' }
$pf = [pscustomobject]@{ ExistingMiKTeX=$null; ExistingTeXLive=$tl; RecommendTeXLive=$false }
$plan = Get-TeXPlan -Preflight $pf -Requested 'auto'
$fails += Check 'existing TeX Live is reused' ($plan.Action -eq 'use-existing' -and $plan.Distro -eq 'texlive')

$pf = [pscustomobject]@{ ExistingMiKTeX=$tl; ExistingTeXLive=$null; RecommendTeXLive=$false }
$plan = Get-TeXPlan -Preflight $pf -Requested 'texlive'
$fails += Check 'existing install beats an explicit -TeXDistribution request' ($plan.Action -eq 'use-existing')

$pf = [pscustomobject]@{ ExistingMiKTeX=$null; ExistingTeXLive=$null; RecommendTeXLive=$true }
$plan = Get-TeXPlan -Preflight $pf -Requested 'auto'
$fails += Check 'Hebrew profile path selects TeX Live' ($plan.Distro -eq 'texlive' -and $plan.Action -eq 'install')

# ---------------------------------------------------------------------------
#  Hebrew detection
# ---------------------------------------------------------------------------
Write-Host "`n=== Hebrew path detection ===" -ForegroundColor Cyan
$hebName  = New-HebrewString @(0x05E0, 0x05D3, 0x05D1)                 # nadav
$hebWord  = New-HebrewString @(0x05D0, 0x05D9, 0x05E0, 0x05E4, 0x05D9) # infi
$hebFinal = New-HebrewString @(0x05EA, 0x05E8, 0x05D2, 0x05D9, 0x05DD) # targilim
$accented = 'Jos' + [char]0x00E9                                       # Jose, acute e

$fails += Check 'detects Hebrew username'      (Test-HasHebrew "C:\Users\$hebName")
$fails += Check 'detects Hebrew folder'        (Test-HasHebrew "C:\Studies\$hebWord\ex1")
$fails += Check 'detects Hebrew final letters' (Test-HasHebrew "D:\$hebFinal")
$fails += Check 'passes clean ASCII path'      (-not (Test-HasHebrew 'C:\Users\nadav\Documents'))
$fails += Check 'passes path with accents'     (-not (Test-HasHebrew "C:\Users\$accented\Docs"))
$fails += Check 'passes empty string'          (-not (Test-HasHebrew ''))

Write-Host ''
if ($fails -eq 0) { Write-Host 'ALL CHECKS PASSED' -ForegroundColor Green }
else { Write-Host "$fails CHECK(S) FAILED" -ForegroundColor Red }
Remove-Item $sandbox -Recurse -Force -ErrorAction SilentlyContinue
exit $fails
