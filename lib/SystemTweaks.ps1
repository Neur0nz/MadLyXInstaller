# SystemTweaks.ps1 - changes that reach outside LyX.
#
# Every function here asks first and reports exactly what it changed, because
# these touch the registry and Defender rather than the user's LyX profile.
#
# Note on fonts: an earlier plan installed the Culmus TTFs system-wide so
# Hebrew would render in the editor. Windows already ships David, FrankRuehl,
# Gisha and Rod, and the classic pdflatex + culmus pipeline gets its output
# fonts from the LaTeX package, not from system fonts. The step was dropped as
# unnecessary. Only the LuaLaTeX/fontspec route would need Culmus CLM fonts.

<#
    Stop Alt+Shift from switching the Windows input language.

    The MadLyX guide's "important note" (p.17) is that Windows must stay on ENG
    at all times, because LyX supplies the Hebrew itself through its keyboard
    map and every shortcut breaks when Windows flips to Hebrew. Alt+Shift is
    trivially easy to hit while reaching for Alt-based shortcuts.

    HKCU\Keyboard Layout\Toggle holds REG_SZ values (writing DWORDs here is a
    common mistake - Windows silently ignores them):
        "1" Alt+Shift (the Windows default)
        "2" Ctrl+Shift
        "3" not assigned
        "4" grave accent

    Per-user, no admin needed, and reversible from
    Settings > Time & Language > Typing > Advanced keyboard settings.
    Win+Space is a separate mechanism and keeps working, so Hebrew stays
    reachable deliberately - just not by accident.
#>
function Disable-LanguageToggleHotkey {
    $key = 'HKCU:\Keyboard Layout\Toggle'

    $languages = @()
    try { $languages = (Get-WinUserLanguageList).LanguageTag } catch {}
    $hasHebrew = $languages -contains 'he' -or $languages -contains 'he-IL'

    $detail = @"
Windows switches input language with Alt+Shift. LyX types Hebrew through its own
keyboard map (F12), so Windows must stay on English - if it flips to Hebrew,
every LyX shortcut stops working. This is the single most common "my shortcuts
broke" problem.

This sets HKCU\Keyboard Layout\Toggle so Alt+Shift no longer switches language.
Win+Space and the taskbar language picker keep working, so you can still type
Hebrew elsewhere. Reversible in Windows Settings at any time.
"@
    if (-not $hasHebrew) {
        $detail += "`nNote: no Hebrew input language is currently installed, so this is precautionary."
    }

    if (-not (Confirm-Action -Question 'Disable the Alt+Shift language switch?' -Detail $detail -Default $false)) {
        Write-Info 'Skipped. Remember to keep Windows on ENG while working in LyX.'
        return $false
    }

    try {
        if (-not (Test-Path $key)) { New-Item -Path $key -Force | Out-Null }
        foreach ($name in @('Language Hotkey', 'Layout Hotkey', 'Hotkey')) {
            Set-ItemProperty -Path $key -Name $name -Value '3' -Type String -Force
        }
        Write-Ok 'Alt+Shift no longer switches input language.'
        Write-Info 'Takes effect for newly started programs - sign out and back in to apply everywhere.'
        return $true
    } catch {
        Write-Err "Could not update the registry: $($_.Exception.Message)"
        return $false
    }
}

<#
    Undo Disable-LanguageToggleHotkey by restoring the Windows default.
#>
function Enable-LanguageToggleHotkey {
    $key = 'HKCU:\Keyboard Layout\Toggle'
    try {
        if (-not (Test-Path $key)) { New-Item -Path $key -Force | Out-Null }
        Set-ItemProperty -Path $key -Name 'Language Hotkey' -Value '1' -Type String -Force
        Set-ItemProperty -Path $key -Name 'Layout Hotkey'   -Value '2' -Type String -Force
        Set-ItemProperty -Path $key -Name 'Hotkey'          -Value '1' -Type String -Force
        Write-Ok 'Restored the default Alt+Shift language switch.'
        return $true
    } catch {
        Write-Err "Could not restore the registry value: $($_.Exception.Message)"
        return $false
    }
}

<#
    Exclude the TeX tree and LyX's working directories from Defender's
    real-time scanning.

    A LaTeX run creates and deletes a lot of small files, and MiKTeX's on-demand
    package installation writes thousands. Real-time scanning of that is a
    measurable share of compile time. Requires administrator rights.
#>
function Add-DefenderExclusions {
    param(
        [Parameter(Mandatory)]$Tex,
        $LyX,
        [string]$UserDir
    )

    if (-not (Get-Command Add-MpPreference -ErrorAction SilentlyContinue)) {
        Write-Info 'Microsoft Defender cmdlets not available - skipping exclusions.'
        return $false
    }
    if (-not (Test-IsAdmin)) {
        Write-Info 'Defender exclusions need administrator rights - skipping.'
        return $false
    }

    $paths = New-Object System.Collections.Generic.List[string]
    # The whole distribution root, not just the bin directory.
    $texRoot = Split-Path (Split-Path $Tex.BinDir)
    if ($Tex.Distro -eq 'texlive') { $texRoot = 'C:\texlive' }
    if ($texRoot -and (Test-Path $texRoot)) { $paths.Add($texRoot) }
    if ($LyX -and (Test-Path $LyX.Root))    { $paths.Add($LyX.Root) }
    if ($UserDir -and (Test-Path $UserDir)) { $paths.Add($UserDir) }

    if ($paths.Count -eq 0) { return $false }

    $detail = @"
LaTeX creates and deletes many small files on every compile, and MiKTeX writes
thousands more when it installs packages on demand. Excluding these folders from
real-time scanning makes compiling noticeably faster.

Folders to exclude:
$($paths | ForEach-Object { "  $_" } | Out-String)
Remove them any time in Windows Security > Virus & threat protection > Exclusions.
"@

    if (-not (Confirm-Action -Question 'Add Windows Defender exclusions for the TeX and LyX folders?' -Detail $detail -Default $false)) {
        Write-Info 'Skipped.'
        return $false
    }

    $added = 0
    foreach ($path in $paths) {
        try { Add-MpPreference -ExclusionPath $path -ErrorAction Stop; $added++ }
        catch { Write-Warn "Could not exclude ${path}: $($_.Exception.Message)" }
    }
    if ($added -gt 0) { Write-Ok "Added $added Defender exclusion(s)."; return $true }
    return $false
}

<#
    Offer to install SumatraPDF, which is what makes SyncTeX forward and
    inverse search possible. LyX's bundled viewer cannot do either.
#>
function Install-SumatraPDF {
    foreach ($candidate in @(
        (Join-Path $env:LOCALAPPDATA 'SumatraPDF\SumatraPDF.exe'),
        'C:\Program Files\SumatraPDF\SumatraPDF.exe',
        'C:\Program Files (x86)\SumatraPDF\SumatraPDF.exe')) {
        if (Test-Path $candidate) { Write-Ok "SumatraPDF already installed at $candidate"; return $candidate }
    }
    if (Get-CommandPath 'SumatraPDF') { return (Get-CommandPath 'SumatraPDF') }

    if (-not (Get-CommandPath 'winget')) { return $null }

    $detail = @"
SumatraPDF is a small, fast PDF viewer that supports SyncTeX. With it, Ctrl+click
in LyX jumps to that exact spot in the PDF, and double-clicking in the PDF jumps
back to the source. LyX's built-in viewer cannot do either.
"@
    if (-not (Confirm-Action -Question 'Install SumatraPDF for jump-to-source support?' -Detail $detail -Default $true)) {
        return $null
    }

    Write-Info 'Installing SumatraPDF...'
    Invoke-Native 'winget' @(
        'install', '--id', 'SumatraPDF.SumatraPDF', '--exact', '--silent',
        '--accept-package-agreements', '--accept-source-agreements'
    ) | Out-Null

    foreach ($candidate in @(
        (Join-Path $env:LOCALAPPDATA 'SumatraPDF\SumatraPDF.exe'),
        'C:\Program Files\SumatraPDF\SumatraPDF.exe')) {
        if (Test-Path $candidate) { Write-Ok 'SumatraPDF installed.'; return $candidate }
    }
    Write-Warn 'SumatraPDF installation could not be confirmed.'
    return $null
}
