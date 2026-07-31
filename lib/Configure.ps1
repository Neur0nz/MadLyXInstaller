# Configure.ps1 - writing LyX settings, shortcuts, templates and macros.
#
# Everything here is idempotent. The reference Python installer opens
# `preferences` and `user.bind` in append mode, so running it twice leaves
# duplicated \kbmap, \visual_cursor and \path_prefix keys and a duplicate bind.
# We remove any line we manage before writing our own block.

$script:PrefBlockStart = '### BEGIN MadLyXInstaller - do not edit inside this block'
$script:PrefBlockEnd   = '### END MadLyXInstaller'

<#
    LyX writes Windows paths with forward slashes in its own preferences file
    (e.g. "C:/Program Files/LyX 2.4/Resources"). Backslashes inside a quoted
    LyX string risk being read as escapes, so normalise before writing.
#>
function ConvertTo-LyXPath {
    param([string]$Path)
    return ($Path -replace '\\', '/')
}

<#
    Rewrite the LyX preferences file.

    $Settings is an ordered hashtable of key -> value where key is the LyX tag
    without its leading backslash, e.g. 'kbmap_primary'. Values already carrying
    their own quoting are written verbatim.

    Colour overrides are passed separately because \set_color takes the colour
    name as part of its identity: \set_color "green" "#b5bd68".
#>
function Set-LyXPreferences {
    param(
        [Parameter(Mandatory)][string]$UserDir,
        [Parameter(Mandatory)][System.Collections.Specialized.OrderedDictionary]$Settings,
        [hashtable]$Colors = @{}
    )

    $prefFile = Join-Path $UserDir 'preferences'
    if (-not (Test-Path $prefFile)) {
        New-Item -ItemType File -Force $prefFile | Out-Null
        Write-Log "created new preferences file at $prefFile"
    } else {
        $backup = Backup-File $prefFile
        if ($backup) { Write-Info "Backed up existing preferences to $(Split-Path $backup -Leaf)" }
    }

    $lines = @(Get-Content $prefFile -Encoding UTF8 -ErrorAction SilentlyContinue)

    # Drop any previous MadLyX block.
    $kept = New-Object System.Collections.Generic.List[string]
    $inBlock = $false
    foreach ($line in $lines) {
        if ($line -eq $script:PrefBlockStart) { $inBlock = $true;  continue }
        if ($line -eq $script:PrefBlockEnd)   { $inBlock = $false; continue }
        if (-not $inBlock) { $kept.Add($line) }
    }

    # Drop stray copies of keys we manage, wherever they appear.
    $managed = @($Settings.Keys)
    $filtered = $kept | Where-Object {
        $line = $_.Trim()
        if ($line -eq '') { return $true }
        foreach ($key in $managed) {
            if ($line -match "^\\$([regex]::Escape($key))(\s|$)") { return $false }
        }
        foreach ($colorName in $Colors.Keys) {
            if ($line -match "^\\set_color\s+`"$([regex]::Escape($colorName))`"") { return $false }
        }
        return $true
    }

    $out = New-Object System.Collections.Generic.List[string]
    foreach ($line in $filtered) { $out.Add($line) }
    while ($out.Count -gt 0 -and $out[$out.Count - 1].Trim() -eq '') { $out.RemoveAt($out.Count - 1) }

    $out.Add('')
    $out.Add($script:PrefBlockStart)
    foreach ($key in $Settings.Keys) { $out.Add("\$key $($Settings[$key])") }
    foreach ($colorName in ($Colors.Keys | Sort-Object)) {
        $out.Add("\set_color `"$colorName`" `"$($Colors[$colorName])`"")
    }
    $out.Add($script:PrefBlockEnd)

    # LyX reads preferences as UTF-8; write without a BOM.
    [IO.File]::WriteAllLines($prefFile, $out, (New-Object Text.UTF8Encoding $false))
    Write-Ok "Wrote $($Settings.Count + $Colors.Count) settings to $prefFile"
}

<#
    Build the settings we apply, given what is actually installed.

    Sources:
      * kbmap / visual_cursor          - MadLyX guide figs. 0.3 and 0.4
      * scroll_below_document          - guide fig. 0.5
      * gui_language english           - guide p.14, its first instruction
      * bind_file cua                  - guide pp.75-77, keeps Ctrl+S/Ctrl+M working
      * preview / autosave / backups   - quality-of-life, not in the guide
#>
function Get-MadLyxSettings {
    param(
        [Parameter(Mandatory)]$Tex,
        [Parameter(Mandatory)]$LyX,
        [Parameter(Mandatory)][string]$UserDir,
        [bool]$EnablePreview = $true,
        [bool]$EnableAutosave = $true
    )

    $settings = [ordered]@{}

    # --- Interface language -------------------------------------------------
    # Every guide and forum answer online is in English, and keyboard navigation
    # of the menus depends on English accelerators.
    $settings['gui_language'] = 'english'

    # --- Hebrew keyboard ----------------------------------------------------
    # LyX does the Hebrew itself; Windows stays on ENG. F12 toggles, bound in
    # the MadLyX bind file.
    $settings['kbmap']           = 'true'
    $settings['kbmap_primary']   = '"null"'
    $settings['kbmap_secondary'] = '"hebrew"'

    # --- RTL cursor ---------------------------------------------------------
    $settings['visual_cursor'] = 'true'

    # --- Editing comfort ----------------------------------------------------
    $settings['scroll_below_document'] = 'true'

    # --- Keep LyX's stock shortcuts -----------------------------------------
    # The MadLyX bind file contains no \bind_file line, so without this the
    # base bindings (Ctrl+S, Ctrl+M) can stop working once user.bind is in place.
    $settings['bind_file'] = '"cua"'

    # --- Let LyX find the TeX binaries --------------------------------------
    $settings['path_prefix'] = "`"$(ConvertTo-LyXPath $Tex.BinDir)`""

    # --- Instant preview ----------------------------------------------------
    # Renders maths and images inline instead of as source boxes. Needs the
    # preview and dvipng packages, installed by Install-HebrewTeXPackages.
    if ($EnablePreview) {
        $settings['preview']              = 'on'
        $settings['preview_scale_factor'] = '1.0'
    }

    # --- Autosave and backups -----------------------------------------------
    if ($EnableAutosave) {
        $backupDir = Join-Path $UserDir 'backups'
        if (-not (Test-Path $backupDir)) { New-Item -ItemType Directory -Force $backupDir | Out-Null }
        $settings['autosave']       = '300'
        $settings['make_backup']    = 'true'
        $settings['backupdir_path'] = "`"$(ConvertTo-LyXPath $backupDir)`""
    }

    return $settings
}

<#
    Muted editor colours for LyX 2.4.

    The guide (p.46, note 4) singles out 2.4's in-editor colours as too loud to
    read comfortably and gives \set_color "green" "#b5bd68" as the fix. These
    extend that to the colours the MadLyX shortcuts actually paint with.
    Editor-only: PDF output is unaffected.
#>
function Get-MadLyxEditorColors {
    param([Parameter(Mandatory)]$LyX)
    # Numeric comparison, not string: '2.10' -lt '2.4' is true as a string.
    if ([version]$LyX.Series -lt [version]'2.4') { return @{} }
    return @{
        'green'   = '#b5bd68'
        'red'     = '#cc6666'
        'blue'    = '#81a2be'
        'magenta' = '#b294bb'
        'cyan'    = '#8abeb7'
        'yellow'  = '#f0c674'
    }
}

<#
    Choose which bundled bind file suits the installed LyX.

    Kali publishes two builds: LyX 2.3 (bind Format 4) and LyX 2.4 (Format 5).
    Anything newer than 2.4 - LyX 2.5 shipped in February 2026 - has no
    dedicated build, so we hand it the 2.4 file. That is safe: LyX detects a
    format mismatch and runs its bind file through prefs2prefs automatically
    (see KeyMap::read in the LyX source), so the shortcuts are converted on
    load rather than rejected.

    Returns the series to install and whether it is an exact match.
#>
function Get-MadLyxBindSeries {
    param([Parameter(Mandatory)]$LyX)

    $series = [version]$LyX.Series
    if ($series -lt [version]'2.4') {
        return @{ Series = '2.3'; Exact = ($series -eq [version]'2.3') }
    }
    return @{ Series = '2.4'; Exact = ($series -eq [version]'2.4') }
}

<#
    Install the MadLyX shortcut file, picking the build that matches the
    installed LyX. The two files are genuinely different: 2.3 is bind Format 4,
    2.4 is Format 5. Installing the wrong one silently loses shortcuts.
#>
function Install-MadLyxBindFile {
    param(
        [Parameter(Mandatory)][string]$UserDir,
        [Parameter(Mandatory)]$LyX,
        [Parameter(Mandatory)][string]$PayloadRoot
    )

    Write-Step 'Installing the MadLyX shortcut file'

    $choice = Get-MadLyxBindSeries -LyX $LyX
    $series = $choice.Series
    $source = Join-Path $PayloadRoot "bind\madlyx-$series.bind"
    if (-not (Test-Path $source)) { Write-Err "Bundled bind file missing: $source"; return $false }

    if (-not $choice.Exact) {
        Write-Info "You are on LyX $($LyX.Version). MadLyX only publishes 2.3 and 2.4 shortcut"
        Write-Info "files, so the $series build is used - LyX converts it on load automatically."
    }

    $bindDir = Join-Path $UserDir 'bind'
    if (-not (Test-Path $bindDir)) { New-Item -ItemType Directory -Force $bindDir | Out-Null }

    $target = Join-Path $bindDir 'user.bind'
    if (Test-Path $target) {
        $backup = Backup-File $target
        Write-Info "Backed up your existing shortcuts to $(Split-Path $backup -Leaf)"
    }

    Copy-Item $source $target -Force
    $count = @(Select-String -Path $target -Pattern '^\\bind').Count
    Write-Ok "Installed $count shortcuts for LyX $series."
    Write-Info 'Check it worked: in a maths box, Alt+W then A should give a Greek alpha.'
    return $true
}

<#
    Install the document templates and point LyX's template browser at them.
    Ctrl+Shift+N then picks them straight out of the New-from-template dialog.
#>
function Install-MadLyxTemplates {
    param(
        [Parameter(Mandatory)][string]$UserDir,
        [Parameter(Mandatory)][string]$PayloadRoot
    )

    Write-Step 'Installing document templates'

    $source = Join-Path $PayloadRoot 'templates'
    if (-not (Test-Path $source)) { Write-Err "Bundled templates missing: $source"; return $null }

    $target = Join-Path $UserDir 'templates'
    if (-not (Test-Path $target)) { New-Item -ItemType Directory -Force $target | Out-Null }

    $installed = 0
    foreach ($file in (Get-ChildItem $source -Filter '*.lyx')) {
        Copy-Item $file.FullName (Join-Path $target $file.Name) -Force
        $installed++
    }

    # LyX uses templates\defaults.lyx as the starting point for a plain File > New.
    $default = Join-Path $source '02-hebrew-article.lyx'
    if (Test-Path $default) { Copy-Item $default (Join-Path $target 'defaults.lyx') -Force }

    Write-Ok "Installed $installed templates into $target"
    Write-Info 'Press Ctrl+Shift+N in LyX to start a document from one of them.'
    return $target
}

<#
    Copy the MadLyX macro files somewhere stable and discoverable.

    These are included into a document by reference rather than copied into it,
    so the location has to be somewhere the user will not casually delete
    (guide, p.41).
#>
function Install-MadLyxMacros {
    param([Parameter(Mandatory)][string]$PayloadRoot)

    Write-Step 'Installing the MadLyX macro files'

    $source = Join-Path $PayloadRoot 'macros'
    if (-not (Test-Path $source)) { Write-Warn 'Bundled macros missing - skipping.'; return $null }

    $target = Join-Path ([Environment]::GetFolderPath('MyDocuments')) 'MadLyX\macros'
    if (-not (Test-Path $target)) { New-Item -ItemType Directory -Force $target | Out-Null }

    foreach ($file in (Get-ChildItem $source -Filter '*.lyx')) {
        Copy-Item $file.FullName (Join-Path $target $file.Name) -Force
    }

    Write-Ok "Macros installed to $target"
    Write-Info 'Include one with Insert > File > Child Document, set to "Input".'
    Write-Info 'Use madlyx-macros-he.lyx for Hebrew documents, -en for English ones.'
    return $target
}

<#
    Copy the shared LaTeX preamble next to the macros.

    This is the collected set of preamble fixes from the guide - muted colours,
    the Longrightarrow repair, disjoint union, listings style, the blank-first-
    page workaround and Hebrew justification. It is already embedded in the
    bundled templates; this standalone copy is for existing documents.
#>
function Install-MadLyxPreamble {
    param([Parameter(Mandatory)][string]$PayloadRoot)

    $source = Join-Path $PayloadRoot 'preamble\madlyx-preamble.tex'
    if (-not (Test-Path $source)) { return $null }

    $target = Join-Path ([Environment]::GetFolderPath('MyDocuments')) 'MadLyX'
    if (-not (Test-Path $target)) { New-Item -ItemType Directory -Force $target | Out-Null }

    Copy-Item $source (Join-Path $target 'madlyx-preamble.tex') -Force
    Write-Ok "Shared preamble copied to $target\madlyx-preamble.tex"
    Write-Info 'Paste its contents into Document > Settings > LaTeX Preamble for older documents.'
    return $target
}

<#
    Configure SyncTeX forward search against SumatraPDF, so Ctrl+click in LyX
    jumps to the matching place in the PDF.

    Placeholders (verified against LyX's LyXRC and the LyX wiki):
      $$t line's source file, $$n line number, $$o output PDF.
#>
function Set-ForwardSearch {
    param(
        [Parameter(Mandatory)][string]$UserDir,
        [string]$SumatraPath,
        $LyX
    )

    if (-not $SumatraPath) { $SumatraPath = Get-CommandPath 'SumatraPDF' }
    if (-not $SumatraPath) {
        foreach ($candidate in @(
            (Join-Path $env:LOCALAPPDATA 'SumatraPDF\SumatraPDF.exe'),
            'C:\Program Files\SumatraPDF\SumatraPDF.exe',
            'C:\Program Files (x86)\SumatraPDF\SumatraPDF.exe')) {
            if (Test-Path $candidate) { $SumatraPath = $candidate; break }
        }
    }
    if (-not $SumatraPath) { return $false }

    $exe = ConvertTo-LyXPath $SumatraPath
    $command = "`\`"$exe`\`" -reuse-instance -forward-search `\`"`$`$t`\`" `$`$n `\`"`$`$o`\`""

    $prefFile = Join-Path $UserDir 'preferences'
    $lines = @(Get-Content $prefFile -Encoding UTF8 -ErrorAction SilentlyContinue) |
             Where-Object { $_ -notmatch '^\\forward_search_pdf(\s|$)' }
    $lines += "\forward_search_pdf `"$command`""
    [IO.File]::WriteAllLines($prefFile, $lines, (New-Object Text.UTF8Encoding $false))

    Write-Ok 'Forward search configured (Ctrl+click in LyX jumps to that spot in the PDF).'

    # Inverse search is set inside SumatraPDF, not LyX, so we can only tell the
    # user the exact string to paste. LyX ships the lyxeditor helper for this.
    if ($LyX) {
        $editor = Join-Path $LyX.Root 'bin\lyxeditor.cmd'
        if (-not (Test-Path $editor)) { $editor = Join-Path $LyX.Root 'bin\lyxeditor.exe' }
        if (Test-Path $editor) {
            Write-Info 'For the reverse direction (PDF -> LyX), open SumatraPDF and set'
            Write-Info 'Settings > Options > "Set inverse search command-line" to:'
            Write-Info "  `"$editor`" `"%f`" %l"
        }
    }
    return $true
}
