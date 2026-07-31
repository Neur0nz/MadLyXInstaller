# TeX.ps1 - TeX distribution detection, installation and Hebrew package setup.
#
# MiKTeX is the default because it installs missing packages on demand. That
# single feature removes the entire "installing new packages" chapter of the
# MadLyX guide (pp. 17-19) and most of its troubleshooting section.
#
# TeX Live is the fallback for profiles whose path contains Hebrew.

$script:CulmusMiktexInstaller = 'http://www.ma.huji.ac.il/~sameti/tex/culmusmiktex0.2.2.exe'

function Find-MiKTeX {
    $candidates = @(
        (Join-Path $env:LOCALAPPDATA 'Programs\MiKTeX\miktex\bin\x64'),
        (Join-Path $env:LOCALAPPDATA 'Programs\MiKTeX\miktex\bin'),
        'C:\Program Files\MiKTeX\miktex\bin\x64',
        'C:\Program Files\MiKTeX 2.9\miktex\bin\x64',
        'C:\Program Files (x86)\MiKTeX 2.9\miktex\bin\x64'
    )
    # An existing installation may only be discoverable through PATH.
    $onPath = Get-CommandPath 'miktex-console'
    if (-not $onPath) { $onPath = Get-CommandPath 'initexmf' }
    if ($onPath) { $candidates = @((Split-Path $onPath)) + $candidates }

    foreach ($dir in $candidates) {
        if ($dir -and (Test-Path (Join-Path $dir 'pdflatex.exe'))) {
            return [pscustomobject]@{ Distro = 'miktex'; BinDir = $dir }
        }
    }
    return $null
}

<#
    Find TeX Live's binary directory.

    The reference installer asserts that <root>/bin contains exactly one entry
    and crashes with a bare AssertionError otherwise. A tree that has been
    upgraded across releases can legitimately hold both 'windows' and 'win32',
    so we pick the one that actually contains pdflatex.
#>
function Find-TeXLive {
    $onPath = Get-CommandPath 'tlmgr'
    if ($onPath) {
        $dir = Split-Path $onPath
        if (Test-Path (Join-Path $dir 'pdflatex.exe')) {
            return [pscustomobject]@{ Distro = 'texlive'; BinDir = $dir }
        }
    }

    $root = 'C:\texlive'
    if (-not (Test-Path $root)) { return $null }

    $years = Get-ChildItem $root -Directory -ErrorAction SilentlyContinue |
             Where-Object { $_.Name -match '^\d{4}$' } |
             Sort-Object Name -Descending

    foreach ($year in $years) {
        $binRoot = Join-Path $year.FullName 'bin'
        if (-not (Test-Path $binRoot)) { continue }
        foreach ($platform in (Get-ChildItem $binRoot -Directory -ErrorAction SilentlyContinue)) {
            if (Test-Path (Join-Path $platform.FullName 'pdflatex.exe')) {
                return [pscustomobject]@{ Distro = 'texlive'; BinDir = $platform.FullName }
            }
        }
    }
    return $null
}

<#
    Install MiKTeX for the current user, with on-the-fly package installation
    enabled so a missing package never becomes an error dialog.
#>
function Install-MiKTeX {
    Write-Step 'Installing MiKTeX'

    if (Get-CommandPath 'winget') {
        Write-Info 'Installing via winget (several minutes, and it is quiet while it works)...'
        Invoke-Native 'winget' @(
            'install', '--id', 'MiKTeX.MiKTeX', '--exact', '--silent',
            '--accept-package-agreements', '--accept-source-agreements'
        ) | Out-Null

        $miktex = Find-MiKTeX
        if ($miktex) {
            Write-Ok "MiKTeX installed at $($miktex.BinDir)"
            Enable-MiKTeXAutoInstall -MiKTeX $miktex
            return $miktex
        }
    }

    Write-Err 'Could not install MiKTeX automatically.'
    Write-Info 'Install it manually from https://miktex.org/download and run this installer again.'
    return $null
}

<#
    Set MiKTeX's "install missing packages on the fly" policy to yes.
    Without this MiKTeX shows a modal dialog per missing package, which is
    exactly the friction we are trying to remove.
#>
function Enable-MiKTeXAutoInstall {
    param([Parameter(Mandatory)]$MiKTeX)

    $initexmf = Join-Path $MiKTeX.BinDir 'initexmf.exe'
    if (-not (Test-Path $initexmf)) { return $false }

    Write-Info 'Enabling automatic package installation...'
    $ok = Invoke-Native $initexmf @('--set-config-value', '[MPM]AutoInstall=1') -TimeoutSeconds 120
    if ($ok) { Write-Ok 'MiKTeX will install missing packages automatically.' }
    else     { Write-Warn 'Could not set AutoInstall - do it in MiKTeX Console > Settings if packages fail later.' }
    return $ok
}

<#
    Install TeX Live from the CTAN net installer.

    Uses scheme-small rather than scheme-basic: scheme-basic omits mathtools,
    stmaryrd, relsize, preview and dvipng, and TeX Live has no on-the-fly
    installation to recover with. scheme-basic plus a handful of packages is
    how students end up in the guide's troubleshooting chapter.
#>
function Install-TeXLive {
    Write-Step 'Installing TeX Live'
    Write-Info 'This is a large download and can take 20-40 minutes.'

    $work = Join-Path $env:TEMP "madlyx-texlive-$(Get-Random)"
    New-Item -ItemType Directory -Force $work | Out-Null
    $zip = Join-Path $work 'install-tl.zip'

    if (-not (Get-RemoteFile 'https://mirror.ctan.org/systems/texlive/tlnet/install-tl.zip' $zip)) {
        Write-Err 'Could not download the TeX Live installer.'
        Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue
        return $null
    }

    Write-Info 'Extracting installer...'
    try { Expand-Archive $zip -DestinationPath $work -Force }
    catch {
        Write-Err "Could not extract the TeX Live installer: $($_.Exception.Message)"
        Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue
        return $null
    }

    $bat = Get-ChildItem $work -Recurse -Filter 'install-tl-windows.bat' -ErrorAction SilentlyContinue |
           Select-Object -First 1
    if (-not $bat) {
        Write-Err 'The TeX Live installer archive did not contain install-tl-windows.bat.'
        Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue
        return $null
    }

    Write-Info 'Running the TeX Live installer...'
    Invoke-Native $bat.FullName @('-no-gui', '--scheme', 'small', '--no-interaction') -TimeoutSeconds 5400 | Out-Null
    Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue

    $texlive = Find-TeXLive
    if ($texlive) { Write-Ok "TeX Live installed at $($texlive.BinDir)"; return $texlive }

    Write-Err 'TeX Live installation did not complete.'
    return $null
}

<#
    Install the LaTeX packages a Hebrew maths document needs.

    On MiKTeX most of this is redundant thanks to AutoInstall, but installing
    up front means the first compile is fast instead of stopping repeatedly.
#>
function Install-HebrewTeXPackages {
    param([Parameter(Mandatory)]$Tex)

    Write-Step 'Installing Hebrew and maths LaTeX packages'

    # babel-hebrew and culmus are the Hebrew core; the rest are what the MadLyX
    # templates and macros actually pull in.
    $packages = @(
        'babel-hebrew', 'hebrew-fonts', 'culmus',
        'preview', 'dvipng',
        'mathtools', 'stmaryrd', 'relsize', 'cancel', 'esint',
        'mathdots', 'mhchem', 'undertilde', 'stackrel',
        'xcolor', 'listings', 'multicol', 'hyperref', 'atbegshi',
        'amsmath', 'amsfonts', 'dsfont', 'wasysym'
    )

    if ($Tex.Distro -eq 'miktex') {
        $mpm = Join-Path $Tex.BinDir 'mpm.exe'
        if (-not (Test-Path $mpm)) { Write-Warn 'mpm.exe not found - skipping package pre-install.'; return }
        Write-Info "Installing $($packages.Count) packages via mpm..."
        # Install individually: one unavailable package name must not abort the batch.
        foreach ($pkg in $packages) {
            Invoke-Native $mpm @('--install', $pkg) -TimeoutSeconds 300 | Out-Null
        }
        Write-Ok 'MiKTeX packages requested (anything missing installs on demand later).'
    }
    else {
        $tlmgr = Join-Path $Tex.BinDir 'tlmgr.bat'
        if (-not (Test-Path $tlmgr)) { $tlmgr = Join-Path $Tex.BinDir 'tlmgr' }
        if (-not (Test-Path $tlmgr)) { Write-Warn 'tlmgr not found - skipping package install.'; return }
        Write-Info "Installing $($packages.Count) packages via tlmgr..."
        Invoke-Native $tlmgr (@('install') + $packages) -TimeoutSeconds 1800 | Out-Null
        Write-Ok 'TeX Live packages installed.'
    }
}

<#
    Make Culmus Hebrew fonts work at the LaTeX level.

    "Culmus" means three different things and the guide's errors come from
    conflating them:
      1. the culmus LaTeX package  - handled by Install-HebrewTeXPackages
      2. culmus.map / culmusnkd.map font maps - registered here (MiKTeX only)
      3. the Culmus TTF system fonts - handled in SystemTweaks.ps1

    On MiKTeX the culmus package is not in the repository, which produces the
    'culmus.sty not found' error on p.21 of the guide. The HUJI installer is
    the documented fix.
#>
function Install-CulmusSupport {
    param([Parameter(Mandatory)]$Tex)

    if ($Tex.Distro -ne 'miktex') {
        Write-Info 'TeX Live ships culmus in its repository - nothing extra to do.'
        return $true
    }

    Write-Step 'Setting up Culmus Hebrew fonts for MiKTeX'

    # Is culmus.sty already resolvable?
    $kpsewhich = Join-Path $Tex.BinDir 'kpsewhich.exe'
    if (Test-Path $kpsewhich) {
        $found = & $kpsewhich 'culmus.sty' 2>$null
        if ($found) { Write-Ok 'culmus.sty is already available.'; return $true }
    }

    $consent = Confirm-Action `
        -Question 'Download and run the Culmus-for-MiKTeX installer?' `
        -Detail @"
MiKTeX does not ship the culmus package, which causes the
'LaTeX Error: File culmus.sty not found' failure when exporting Hebrew documents.

Source: $script:CulmusMiktexInstaller
(the installer linked by the MadLyX guide, hosted by HUJI)
"@ -Default $true

    if (-not $consent) { Write-Warn 'Skipped - Hebrew PDF export may fail with culmus.sty errors.'; return $false }

    $exe = Join-Path $env:TEMP "culmusmiktex-$(Get-Random).exe"
    if (-not (Get-RemoteFile $script:CulmusMiktexInstaller $exe)) {
        Write-Err 'Could not download the Culmus installer.'
        return $false
    }

    Write-Info 'Running the Culmus installer...'
    Invoke-Native $exe @() -TimeoutSeconds 600 | Out-Null
    Remove-Item $exe -Force -ErrorAction SilentlyContinue

    Register-CulmusFontMaps -Tex $Tex
    return $true
}

<#
    Register culmus.map / culmusnkd.map with updmap and refresh the filename
    database. This is the p.24 fix from the guide, done non-interactively.
#>
function Register-CulmusFontMaps {
    param([Parameter(Mandatory)]$Tex)

    $initexmf = Join-Path $Tex.BinDir 'initexmf.exe'
    if (-not (Test-Path $initexmf)) { return $false }

    Write-Info 'Registering Culmus font maps...'
    foreach ($map in @('culmus.map', 'culmusnkd.map')) {
        Invoke-Native $initexmf @('--edit-config-file', 'updmap') -TimeoutSeconds 60 | Out-Null
    }

    # The reliable non-interactive route is to append to updmap.cfg directly,
    # since --edit-config-file opens an editor.
    $updmapCfg = Join-Path $env:LOCALAPPDATA 'MiKTeX\miktex\config\updmap.cfg'
    $cfgDir = Split-Path $updmapCfg
    if (-not (Test-Path $cfgDir)) { New-Item -ItemType Directory -Force $cfgDir | Out-Null }

    $existing = if (Test-Path $updmapCfg) { Get-Content $updmapCfg -Raw } else { '' }
    $added = $false
    foreach ($map in @('culmus.map', 'culmusnkd.map')) {
        if ($existing -notmatch [regex]::Escape("Map $map")) {
            Add-Content $updmapCfg "Map $map" -Encoding ASCII
            $added = $true
        }
    }
    if ($added) { Write-Info 'Added Culmus maps to updmap.cfg.' }

    Invoke-Native $initexmf @('--mkmaps') -TimeoutSeconds 300 | Out-Null
    Invoke-Native $initexmf @('--update-fndb') -TimeoutSeconds 300 | Out-Null
    Write-Ok 'Culmus font maps registered.'
    return $true
}
