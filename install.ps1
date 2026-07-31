<#
.SYNOPSIS
    Installs and configures LyX for Hebrew, following "The MadLyX" by Kali.

.DESCRIPTION
    Installs a TeX distribution and LyX, then applies the full Hebrew setup from
    the guide: keyboard map, visual cursor, English interface, the MadLyX
    shortcut file, templates and macros. Adds quality-of-life settings the guide
    does not cover (instant preview, autosave, jump-to-source), and finishes by
    compiling a Hebrew test document to prove the chain works.

    Safe to re-run. Existing preferences and shortcuts are backed up, and
    settings are rewritten rather than appended.

.PARAMETER Doctor
    Only run diagnostics. Changes nothing.

.PARAMETER Unattended
    Never prompt. System-level changes (registry, Defender) are skipped rather
    than assumed.

.PARAMETER TeXDistribution
    'auto' (default), 'miktex', or 'texlive'. Auto picks MiKTeX unless the user
    profile path contains Hebrew, in which case TeX Live is used.

.PARAMETER SkipSmokeTest
    Skip the final test compile.

.EXAMPLE
    .\install.ps1

.EXAMPLE
    .\install.ps1 -Doctor
#>
[CmdletBinding()]
param(
    [switch]$Doctor,
    [switch]$Unattended,
    [ValidateSet('auto', 'miktex', 'texlive')][string]$TeXDistribution = 'auto',
    [switch]$SkipSmokeTest,
    [switch]$SkipSystemTweaks,
    [switch]$NoElevate
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$RepoRoot    = Split-Path -Parent $MyInvocation.MyCommand.Path
$PayloadRoot = Join-Path $RepoRoot 'payload'

foreach ($module in @('Common', 'Preflight', 'LyX', 'TeX', 'Configure', 'SystemTweaks', 'Doctor')) {
    . (Join-Path $RepoRoot "lib\$module.ps1")
}

$script:MadLyxUnattended = [bool]$Unattended

$logDir = Join-Path $env:LOCALAPPDATA 'MadLyXInstaller'
Initialize-MadLyxLog (Join-Path $logDir "install-$(Get-Date -Format 'yyyyMMdd-HHmmss').log")

function Show-Banner {
    Write-Host ''
    Write-Host '  MadLyX Installer' -ForegroundColor Cyan
    Write-Host '  LyX + Hebrew, set up the way the MadLyX guide describes.' -ForegroundColor DarkGray
    Write-Host ''
}

# ---------------------------------------------------------------------------
#  Doctor mode
# ---------------------------------------------------------------------------
if ($Doctor) {
    Show-Banner
    $problemCount = Invoke-MadLyxDoctor -PayloadRoot $PayloadRoot
    exit ([int]($problemCount -gt 0))
}

Show-Banner

# ---------------------------------------------------------------------------
#  0. Elevation
#
#  Most of the work does not need administrator rights: MiKTeX installs per
#  user, and the Alt+Shift fix is under HKCU. Two things do - installing LyX
#  machine-wide, and Defender exclusions - so we offer to restart elevated
#  rather than either demanding it or silently skipping those steps.
# ---------------------------------------------------------------------------
# Test-CanPrompt matters here beyond politeness: without a live console, a UAC
# dialog would appear with nobody there to accept it.
if (-not (Test-IsAdmin) -and -not $NoElevate -and $PSCommandPath -and (Test-CanPrompt)) {
    $wantsElevation = Confirm-Action `
        -Question 'Restart with administrator rights?' `
        -Detail @'
Not required, but without it these steps are skipped:
  - installing LyX for all users (a per-user install still works)
  - Windows Defender exclusions, which speed up compiling

Everything else - MiKTeX, the Hebrew setup, shortcuts, templates and the
Alt+Shift fix - works fine without administrator rights.
'@ -Default $true

    if ($wantsElevation) {
        $argList = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$PSCommandPath`"")
        foreach ($name in $PSBoundParameters.Keys) {
            $value = $PSBoundParameters[$name]
            if ($value -is [switch]) { if ($value.IsPresent) { $argList += "-$name" } }
            else { $argList += @("-$name", "`"$value`"") }
        }
        try {
            Write-Info 'Restarting with administrator rights...'
            $elevated = Start-Process -FilePath (Get-Process -Id $PID).Path `
                                      -ArgumentList $argList -Verb RunAs -PassThru
            $elevated.WaitForExit()
            exit $elevated.ExitCode
        } catch {
            Write-Warn 'Elevation was declined - continuing without administrator rights.'
        }
    } else {
        Write-Info 'Continuing without administrator rights.'
    }
}

# ---------------------------------------------------------------------------
#  1. Preflight
# ---------------------------------------------------------------------------
$preflight = Test-Preflight

if ($preflight.HebrewProfile -and -not $Unattended) {
    $continue = Confirm-Action `
        -Question 'Continue anyway?' `
        -Detail @'
Hebrew in your user folder path is the leading cause of "LyX will not export to
PDF". The installer will use TeX Live, which handles it better, but the reliable
fix is a Windows account with an English user name.

You can continue and keep your documents somewhere like C:\Studies.
'@ -Default $true
    if (-not $continue) { Write-Info 'Stopped at your request. Nothing was changed.'; exit 2 }
}

# ---------------------------------------------------------------------------
#  2. TeX distribution
# ---------------------------------------------------------------------------
$plan = Get-TeXPlan -Preflight $preflight -Requested $TeXDistribution
$tex  = $plan.Info

if ($plan.Action -eq 'use-existing') {
    Write-Step "Using the TeX distribution you already have ($($plan.Distro))"
    Write-Info $tex.BinDir
    if ($plan.Distro -eq 'miktex') { Enable-MiKTeXAutoInstall -MiKTeX $tex | Out-Null }
} else {
    $why = if ($preflight.RecommendTeXLive) { 'Hebrew in your profile path' } else { 'installs missing packages automatically' }
    Write-Info "Chosen distribution: $($plan.Distro) ($why)."
    $tex = if ($plan.Distro -eq 'miktex') { Install-MiKTeX } else { Install-TeXLive }
}

if (-not $tex) {
    Write-Err 'Cannot continue without a TeX distribution.'
    Write-Info "Log: $script:MadLyxLogFile"
    exit 1
}

# ---------------------------------------------------------------------------
#  3. LyX
# ---------------------------------------------------------------------------
$lyx = $preflight.ExistingLyX
if (-not $lyx) { $lyx = Install-LyX }
if (-not $lyx) {
    Write-Err 'Cannot continue without LyX.'
    Write-Info "Log: $script:MadLyxLogFile"
    exit 1
}

$userDir = Initialize-LyXUserDir -LyX $lyx
if (-not $userDir) {
    Write-Err 'Could not find or create the LyX settings folder.'
    Write-Info 'Open LyX manually once, close it, then run this installer again.'
    exit 1
}

# ---------------------------------------------------------------------------
#  4. LaTeX packages and Hebrew font support
# ---------------------------------------------------------------------------
Install-HebrewTeXPackages -Tex $tex
Install-CulmusSupport -Tex $tex | Out-Null

# ---------------------------------------------------------------------------
#  5. Optional viewer for jump-to-source
# ---------------------------------------------------------------------------
$sumatra = $null
if (-not $SkipSystemTweaks) { $sumatra = Install-SumatraPDF }

# ---------------------------------------------------------------------------
#  6. LyX settings, shortcuts, templates, macros
# ---------------------------------------------------------------------------
Write-Step 'Applying the MadLyX settings'

$settings = Get-MadLyxSettings -Tex $tex -LyX $lyx -UserDir $userDir
$colors   = Get-MadLyxEditorColors -LyX $lyx

$templateDir = Join-Path $userDir 'templates'
$settings['template_path'] = "`"$(ConvertTo-LyXPath $templateDir)`""

Set-LyXPreferences -UserDir $userDir -Settings $settings -Colors $colors
if ($colors.Count -gt 0) { Write-Info "Applied $($colors.Count) muted editor colours for LyX $($lyx.Series)." }

Install-MadLyxBindFile  -UserDir $userDir -LyX $lyx -PayloadRoot $PayloadRoot | Out-Null
Install-MadLyxTemplates -UserDir $userDir -PayloadRoot $PayloadRoot | Out-Null
Install-MadLyxMacros    -PayloadRoot $PayloadRoot | Out-Null
Install-MadLyxPreamble  -PayloadRoot $PayloadRoot | Out-Null

if ($sumatra) { Set-ForwardSearch -UserDir $userDir -SumatraPath $sumatra -LyX $lyx | Out-Null }

# ---------------------------------------------------------------------------
#  7. System-level changes (each one asks first)
# ---------------------------------------------------------------------------
if (-not $SkipSystemTweaks) {
    Write-Step 'Optional changes outside LyX'
    Disable-LanguageToggleHotkey | Out-Null
    Add-DefenderExclusions -Tex $tex -LyX $lyx -UserDir $userDir | Out-Null
}

# ---------------------------------------------------------------------------
#  8. Reconfigure and smoke test
# ---------------------------------------------------------------------------
Invoke-LyXReconfigure -LyX $lyx | Out-Null

$smokeResult = $null
if (-not $SkipSmokeTest) { $smokeResult = Invoke-SmokeTest -LyX $lyx -PayloadRoot $PayloadRoot }

# ---------------------------------------------------------------------------
#  9. Summary
# ---------------------------------------------------------------------------
Write-Host ''
Write-Host '  Done' -ForegroundColor Cyan
Write-Host '  ----' -ForegroundColor Cyan
Write-Host "  LyX $($lyx.Version)  |  $($tex.Distro)  |  settings in $userDir" -ForegroundColor Gray
Write-Host ''
Write-Host '  Things to know:' -ForegroundColor White
Write-Host '    - Press F12 inside LyX to switch between Hebrew and English.' -ForegroundColor Gray
Write-Host '    - Keep Windows itself on ENG. LyX does the Hebrew.' -ForegroundColor Gray
Write-Host '    - Ctrl+Shift+N starts a document from a MadLyX template.' -ForegroundColor Gray
Write-Host '    - Check the shortcuts loaded: in a maths box, Alt+W then A gives a Greek alpha.' -ForegroundColor Gray
Write-Host '    - Keep document folders free of Hebrew characters.' -ForegroundColor Gray
Write-Host ''

if ($smokeResult -eq $false) {
    Write-Host '  The test compile failed - see the guidance above.' -ForegroundColor Yellow
    Write-Host "  Re-check any time with:  .\install.ps1 -Doctor" -ForegroundColor Yellow
    Write-Host ''
    Write-Info "Log: $script:MadLyxLogFile"
    exit 1
}

Write-Host "  Log: $script:MadLyxLogFile" -ForegroundColor DarkGray
Write-Host ''
exit 0
