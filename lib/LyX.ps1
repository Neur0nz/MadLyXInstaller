# LyX.ps1 - locating, installing and first-running LyX.

<#
    Find the installed LyX program directory.

    Every candidate is probed with Test-Path first. The reference Python
    installer calls os.listdir() on four hard-coded directories, one of which
    (%LOCALAPPDATA%\Programs) frequently does not exist, and raises an uncaught
    FileNotFoundError before it reaches the directory that does.

    Returns $null when LyX is not installed, otherwise an object with:
      Root       - install directory
      Exe        - full path to LyX.exe
      Version    - [version] parsed from LyX.exe, e.g. 2.4.4
      Series     - '2.3' or '2.4', used to pick the matching bind file
      UserDirName- expected %APPDATA% folder name, e.g. 'LyX2.4'
#>
function Find-LyXInstallation {
    $bases = @(
        (Join-Path $env:LOCALAPPDATA 'Programs'),
        $env:LOCALAPPDATA,
        ${env:ProgramFiles},
        ${env:ProgramFiles(x86)}
    ) | Where-Object { $_ -and (Test-Path $_) }

    $candidates = foreach ($base in $bases) {
        Get-ChildItem $base -Directory -Filter 'LyX*' -ErrorAction SilentlyContinue
    }

    foreach ($dir in ($candidates | Sort-Object Name -Descending)) {
        $exe = Join-Path $dir.FullName 'bin\LyX.exe'
        if (-not (Test-Path $exe)) { continue }

        $raw = (Get-Item $exe).VersionInfo.ProductVersion
        if (-not $raw) { $raw = (Get-Item $exe).VersionInfo.FileVersion }
        $version = $null
        if ($raw -and ($raw -match '(\d+)\.(\d+)(\.(\d+))?')) {
            # Spelled out rather than using ?? : the null-coalescing operator is
            # PowerShell 7 only, and this has to run under Windows PowerShell 5.1.
            $patch = if ($Matches.ContainsKey(4) -and $Matches[4]) { $Matches[4] } else { '0' }
            $version = [version]("{0}.{1}.{2}" -f $Matches[1], $Matches[2], $patch)
        }

        # Fall back to the folder name (LyX 2.4.4) when the binary has no version resource.
        if (-not $version -and $dir.Name -match '(\d+)\.(\d+)') {
            $version = [version]("{0}.{1}.0" -f $Matches[1], $Matches[2])
        }
        if (-not $version) { continue }

        $series = "$($version.Major).$($version.Minor)"
        return [pscustomobject]@{
            Root        = $dir.FullName
            Exe         = $exe
            Version     = $version
            Series      = $series
            UserDirName = "LyX$series"
        }
    }
    return $null
}

<#
    Find the LyX *user* directory (%APPDATA%\LyX2.4), where preferences,
    bind\ and templates\ live. Prefers the folder matching the installed
    version, then falls back to the newest LyX* folder present.
#>
function Find-LyXUserDir {
    param($LyX)
    $roaming = $env:APPDATA
    if (-not (Test-Path $roaming)) { return $null }

    if ($LyX) {
        $exact = Join-Path $roaming $LyX.UserDirName
        if (Test-Path $exact) { return $exact }
    }
    $found = @(Get-ChildItem $roaming -Directory -Filter 'LyX*' -ErrorAction SilentlyContinue |
               Sort-Object Name -Descending)
    # Not $found?.FullName - null-conditional access is PowerShell 7 only.
    if ($found.Count -gt 0) { return $found[0].FullName }
    return $null
}

<#
    Install LyX via winget, falling back to Chocolatey.

    winget needs --accept-source-agreements: on a machine that has never run
    winget, the source agreement prompt blocks forever in a non-interactive run.
#>
function Install-LyX {
    Write-Step 'Installing LyX'

    if (Get-CommandPath 'winget') {
        Write-Info 'Installing via winget (this can take a few minutes)...'
        Invoke-Native 'winget' @(
            'install', '--id', 'LyX.LyX', '--exact', '--silent',
            '--accept-package-agreements', '--accept-source-agreements'
        ) | Out-Null

        # winget's exit code is not a reliable success signal, so confirm by
        # locating the binary - but wait for it. winget returns once it has
        # handed off to the NSIS installer, which is still unpacking, so an
        # immediate check reports "not installed" for an install that succeeds
        # moments later and wrongly falls through to Chocolatey.
        Write-Info 'Waiting for the installation to finish settling...'
        $lyx = Wait-For { Find-LyXInstallation } -TimeoutSeconds 180
        if ($lyx) { Write-Ok "LyX $($lyx.Version) installed."; return $lyx }

        Write-Warn 'winget did not produce a working LyX installation within 3 minutes.'
    }

    if (Get-CommandPath 'choco') {
        Write-Info 'Trying Chocolatey...'
        if (Invoke-Native 'choco' @('install', 'lyx', '-y', '--no-progress')) {
            $lyx = Find-LyXInstallation
            if ($lyx) { Write-Ok "LyX $($lyx.Version) installed."; return $lyx }
        }
    }

    Write-Err 'Could not install LyX automatically.'
    Write-Info 'Download and install it manually from https://www.lyx.org/Download'
    Write-Info 'then run this installer again - it will pick up from here.'
    return $null
}

<#
    Start LyX once so it creates its user directory, then close it.

    The reference installer sleeps a fixed 10 seconds and then kills the
    process, which both races on a cold first start and risks killing LyX
    mid-write. We poll for the preferences directory instead and ask for a
    graceful close first.
#>
function Initialize-LyXUserDir {
    param(
        [Parameter(Mandatory)]$LyX,
        [int]$TimeoutSeconds = 90
    )

    $existing = Find-LyXUserDir -LyX $LyX
    if ($existing -and (Test-Path $existing)) { return $existing }

    Write-Step 'Starting LyX once to create its settings folder'
    Write-Info 'A LyX window will open and close by itself. No need to touch it.'

    $proc = Start-Process -FilePath $LyX.Exe -PassThru
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $userDir = $null

    while ((Get-Date) -lt $deadline) {
        Start-Sleep -Milliseconds 750
        $userDir = Find-LyXUserDir -LyX $LyX
        # Wait for the directory *and* a settled preferences file, so we never
        # kill LyX while it is still writing its initial configuration.
        if ($userDir -and (Test-Path (Join-Path $userDir 'preferences'))) {
            Start-Sleep -Seconds 2
            break
        }
    }

    if (-not $proc.HasExited) {
        try { $proc.CloseMainWindow() | Out-Null; Start-Sleep -Seconds 3 } catch {}
        if (-not $proc.HasExited) { try { $proc.Kill() } catch {} }
    }
    $proc.WaitForExit(10000) | Out-Null

    $userDir = Find-LyXUserDir -LyX $LyX
    if ($userDir) {
        Write-Ok "LyX settings folder: $userDir"
        return $userDir
    }

    Write-Warn 'LyX did not create its settings folder automatically.'
    if (Test-CanPrompt) {
        Write-Host '    Please open LyX manually, then close it, and press Enter here.' -ForegroundColor White
        try { Read-Host '    Press Enter when done' | Out-Null } catch { }
        $userDir = Find-LyXUserDir -LyX $LyX
    }
    return $userDir
}

<#
    Ask LyX to rescan its TeX installation (the GUI's Tools -> Reconfigure).
    Needed after installing TeX packages or fonts; the guide reaches for it
    repeatedly in the troubleshooting chapter.
#>
function Invoke-LyXReconfigure {
    param([Parameter(Mandatory)]$LyX)

    Write-Info 'Asking LyX to rescan the TeX installation (may take a few minutes)...'
    $configure = Join-Path $LyX.Root 'Resources\configure.py'
    $python    = Join-Path $LyX.Root 'Python\python.exe'

    # LyX ships its own Python interpreter on Windows, so this needs nothing
    # installed on the user's machine.
    if ((Test-Path $configure) -and (Test-Path $python)) {
        $userDir = Find-LyXUserDir -LyX $LyX
        if ($userDir) {
            $ok = Invoke-Native $python @($configure, "--binary-dir=$($LyX.Root)\bin") -TimeoutSeconds 600
            if ($ok) { Write-Ok 'LyX reconfigured.'; return $true }
        }
    }
    Write-Warn 'Could not reconfigure automatically - use Tools > Reconfigure inside LyX if something looks wrong.'
    return $false
}
