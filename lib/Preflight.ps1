# Preflight.ps1 - environment checks run before anything is installed.
#
# The single most common cause of "LyX won't export to PDF" is Hebrew (or any
# non-ASCII) text in the path to the document or the user profile. The MadLyX
# guide opens with this warning and returns to it three more times in the
# troubleshooting chapter. We check it up front and steer accordingly.

function Test-Preflight {
    [CmdletBinding()]
    param()

    $result = [ordered]@{
        Ok               = $true
        HebrewProfile    = $false
        IsAdmin          = (Test-IsAdmin)
        FreeSpaceGB      = 0
        ExistingLyX      = $null
        ExistingMiKTeX   = $null
        ExistingTeXLive  = $null
        RecommendTeXLive = $false
    }

    Write-Step 'Checking your system'

    # --- Windows / PowerShell ------------------------------------------------
    $os = Get-CimInstance Win32_OperatingSystem
    Write-Info "$($os.Caption) (build $($os.BuildNumber)), PowerShell $($PSVersionTable.PSVersion)"

    # --- Hebrew in the user profile path ------------------------------------
    # This cannot be fixed in place: Windows offers no supported way to rename a
    # user profile directory. TeX Live tolerates it better than MiKTeX, so we
    # note it and let Get-TeXPlan switch distributions.
    if (Test-HasHebrew $env:USERPROFILE) {
        $result.HebrewProfile    = $true
        $result.RecommendTeXLive = $true
        Write-Warn "Your user folder contains Hebrew characters:"
        Write-Warn "  $env:USERPROFILE"
        Write-Warn 'This is the most common cause of PDF export failures in LyX.'
        Write-Info 'We will install TeX Live instead of MiKTeX, which copes better,'
        Write-Info 'but the reliable fix is a Windows user account with an English name.'
    } else {
        Write-Ok "User profile path is ASCII-clean ($env:USERPROFILE)"
    }

    # --- Disk space ----------------------------------------------------------
    # MiKTeX basic + LyX is well under 2 GB, but on-the-fly package installation
    # grows the tree over a semester. TeX Live full is far larger again.
    $systemDrive = ($env:SystemDrive).TrimEnd(':')
    $drive = @(Get-PSDrive $systemDrive -ErrorAction SilentlyContinue)
    $free = if ($drive.Count -gt 0) { $drive[0].Free } else { $null }
    if ($free) {
        $result.FreeSpaceGB = [math]::Round($free / 1GB, 1)
        if ($result.FreeSpaceGB -lt 3) {
            Write-Warn "Only $($result.FreeSpaceGB) GB free on $($env:SystemDrive) - 3 GB or more is recommended."
        } else {
            Write-Ok "$($result.FreeSpaceGB) GB free on $env:SystemDrive"
        }
    }

    # --- Existing installations ---------------------------------------------
    $result.ExistingLyX     = Find-LyXInstallation
    $result.ExistingMiKTeX  = Find-MiKTeX
    $result.ExistingTeXLive = Find-TeXLive

    if ($result.ExistingLyX)     { Write-Ok "LyX $($result.ExistingLyX.Version) already installed at $($result.ExistingLyX.Root)" }
    else                         { Write-Info 'LyX is not installed yet.' }

    if ($result.ExistingMiKTeX)  { Write-Ok "MiKTeX found at $($result.ExistingMiKTeX.BinDir)" }
    if ($result.ExistingTeXLive) { Write-Ok "TeX Live found at $($result.ExistingTeXLive.BinDir)" }
    if (-not $result.ExistingMiKTeX -and -not $result.ExistingTeXLive) {
        Write-Info 'No TeX distribution found yet.'
    }

    # --- Admin ---------------------------------------------------------------
    if ($result.IsAdmin) { Write-Ok 'Running with administrator rights.' }
    else { Write-Warn 'Not running as administrator - some steps will be skipped or offered again later.' }

    return [pscustomobject]$result
}

<#
    Decide which TeX distribution to use.

    Order of preference, following the MadLyX guide:
      1. Whatever is already installed (never install a second distribution).
      2. MiKTeX - it installs missing packages on the fly, which removes the
         entire "missing package" troubleshooting chapter of the guide.
      3. TeX Live - when the profile path contains Hebrew, or the caller asked
         for it explicitly.
#>
function Get-TeXPlan {
    param(
        [Parameter(Mandatory)]$Preflight,
        [ValidateSet('auto', 'miktex', 'texlive')][string]$Requested = 'auto'
    )

    if ($Preflight.ExistingMiKTeX)  { return @{ Distro = 'miktex';  Action = 'use-existing'; Info = $Preflight.ExistingMiKTeX } }
    if ($Preflight.ExistingTeXLive) { return @{ Distro = 'texlive'; Action = 'use-existing'; Info = $Preflight.ExistingTeXLive } }

    $distro = switch ($Requested) {
        'miktex'  { 'miktex' }
        'texlive' { 'texlive' }
        default   { if ($Preflight.RecommendTeXLive) { 'texlive' } else { 'miktex' } }
    }
    return @{ Distro = $distro; Action = 'install'; Info = $null }
}
