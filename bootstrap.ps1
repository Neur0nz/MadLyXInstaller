<#
.SYNOPSIS
    One-line web installer for MadLyXInstaller.

.DESCRIPTION
    This is the only script in the repository that is safe to pipe into iex.
    install.ps1 is not: it dot-sources lib\*.ps1 and reads payload\, and under
    `iex` there is no script on disk, so $PSScriptRoot is empty and it fails
    immediately.

    This script is fully self-contained. It downloads the repository archive,
    extracts it to a temporary folder, clears the mark-of-the-web from the
    extracted files, and runs install.ps1 from there.

.EXAMPLE
    # Plain install
    irm https://raw.githubusercontent.com/Neur0nz/MadLyXInstaller/main/bootstrap.ps1 | iex

.EXAMPLE
    # With arguments - note the scriptblock form, `irm | iex` cannot take them
    & ([scriptblock]::Create((irm https://raw.githubusercontent.com/Neur0nz/MadLyXInstaller/main/bootstrap.ps1))) -Doctor
#>
[CmdletBinding()]
param(
    # GitHub repository to fetch, as owner/name. Change this to your fork.
    [string]$Repo = 'Neur0nz/MadLyXInstaller',
    [string]$Branch = 'main',

    # Passed straight through to install.ps1.
    [switch]$Doctor,
    [switch]$Unattended,
    [ValidateSet('auto', 'miktex', 'texlive')][string]$TeXDistribution = 'auto',
    [switch]$SkipSmokeTest,
    [switch]$SkipSystemTweaks,
    [switch]$NoElevate,

    # Keep the downloaded copy instead of deleting it afterwards.
    [switch]$KeepFiles
)

$ErrorActionPreference = 'Stop'

# Windows PowerShell 5.1 still negotiates TLS 1.0/1.1 by default on some
# builds, and GitHub refuses those. PowerShell 7 already defaults to 1.2+.
try {
    [Net.ServicePointManager]::SecurityProtocol =
        [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch { }

# Everything lives in a function so that no `exit` ever runs at top level.
# Invoke-Expression executes in the caller's scope, so a bare `exit` here would
# close the user's PowerShell window - which is exactly the window they are
# watching the install in.
function Invoke-MadLyxBootstrap {
    param($Repo, $Branch, $Doctor, $Unattended, $TeXDistribution,
          $SkipSmokeTest, $SkipSystemTweaks, $NoElevate, $KeepFiles)

Write-Host ''
Write-Host '  MadLyX Installer - bootstrap' -ForegroundColor Cyan
Write-Host ''

if ($Repo -like 'OWNER/*') {
    Write-Host '  This bootstrap has not been pointed at a repository yet.' -ForegroundColor Red
    Write-Host '  Edit the $Repo default in bootstrap.ps1, or pass -Repo owner/name.' -ForegroundColor Red
    Write-Host ''
    return 1
}

$work = Join-Path $env:TEMP "madlyx-bootstrap-$(Get-Date -Format 'yyyyMMdd-HHmmss')"
New-Item -ItemType Directory -Force $work | Out-Null
$zip = Join-Path $work 'repo.zip'
$url = "https://codeload.github.com/$Repo/zip/refs/heads/$Branch"

try {
    Write-Host "  Downloading $Repo ($Branch)..." -ForegroundColor Gray
    $previousProgress = $ProgressPreference
    $ProgressPreference = 'SilentlyContinue'
    try {
        Invoke-WebRequest -Uri $url -OutFile $zip -UseBasicParsing -TimeoutSec 300
    } finally {
        $ProgressPreference = $previousProgress
    }

    $sizeKB = [math]::Round((Get-Item $zip).Length / 1KB, 0)
    Write-Host "  Downloaded $sizeKB KB. Extracting..." -ForegroundColor Gray
    Expand-Archive -Path $zip -DestinationPath $work -Force

    # GitHub archives extract into <repo>-<branch>\
    $extracted = Get-ChildItem $work -Directory | Select-Object -First 1
    if (-not $extracted) { throw 'The downloaded archive did not contain a folder.' }

    $installer = Join-Path $extracted.FullName 'install.ps1'
    if (-not (Test-Path $installer)) { throw "install.ps1 not found in the downloaded archive." }

    # Files that arrive from the internet carry a Zone.Identifier stream, which
    # makes them fail under the RemoteSigned execution policy. Clear it.
    Get-ChildItem $extracted.FullName -Recurse -File -Include '*.ps1', '*.psm1' |
        ForEach-Object { try { Unblock-File $_.FullName } catch { } }

    # Windows PowerShell 5.1 leaves CurrentUser and LocalMachine Undefined out of
    # the box, which resolves to Restricted - no script may be loaded from disk at
    # all. `irm | iex` itself still runs, because that is in-memory evaluation, so
    # the failure only appears at the moment we invoke install.ps1.
    #
    # Process scope outranks CurrentUser and LocalMachine and lasts only for this
    # process. It cannot override a Group Policy setting, but neither can passing
    # -ExecutionPolicy on a command line, so this covers the same ground.
    $policyBefore = Get-ExecutionPolicy
    if ($policyBefore -in @('Restricted', 'AllSigned', 'Undefined')) {
        Write-Host "  Execution policy is $policyBefore; allowing scripts for this process only..." -ForegroundColor Gray
        try {
            Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force -ErrorAction Stop
        } catch {
            $gp = Get-ExecutionPolicy -List |
                  Where-Object { $_.Scope -in 'MachinePolicy', 'UserPolicy' -and $_.ExecutionPolicy -ne 'Undefined' }
            if ($gp) {
                throw ("PowerShell script execution is locked down by Group Policy " +
                       "($($gp[0].Scope) = $($gp[0].ExecutionPolicy)). This cannot be " +
                       "overridden from a script. Ask whoever manages this machine, or " +
                       "install manually using the steps below.")
            }
            throw "Could not allow script execution for this process: $($_.Exception.Message)"
        }
    }

    Write-Host "  Running installer..." -ForegroundColor Gray

    $forward = @{}
    if ($Doctor)           { $forward['Doctor']           = $true }
    if ($Unattended)       { $forward['Unattended']       = $true }
    if ($SkipSmokeTest)    { $forward['SkipSmokeTest']    = $true }
    if ($SkipSystemTweaks) { $forward['SkipSystemTweaks'] = $true }
    if ($NoElevate)        { $forward['NoElevate']        = $true }
    if ($TeXDistribution -ne 'auto') { $forward['TeXDistribution'] = $TeXDistribution }

    & $installer @forward
    $exitCode = if ($null -ne $LASTEXITCODE) { $LASTEXITCODE } else { 0 }
}
catch {
    Write-Host ''
    Write-Host "  Bootstrap failed: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host ''
    Write-Host '  You can install manually instead:' -ForegroundColor Gray
    Write-Host "    1. Download https://github.com/$Repo/archive/refs/heads/$Branch.zip" -ForegroundColor Gray
    Write-Host '    2. Extract it, right-click the folder > Properties > Unblock if offered' -ForegroundColor Gray
    Write-Host '    3. powershell -ExecutionPolicy Bypass -File .\install.ps1' -ForegroundColor Gray
    Write-Host ''
    $exitCode = 1
}
finally {
    if ($KeepFiles) {
        Write-Host "  Downloaded copy kept at: $work" -ForegroundColor DarkGray
    } else {
        Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue
    }
}

    return $exitCode
}

$code = Invoke-MadLyxBootstrap -Repo $Repo -Branch $Branch -Doctor $Doctor `
    -Unattended $Unattended -TeXDistribution $TeXDistribution `
    -SkipSmokeTest $SkipSmokeTest -SkipSystemTweaks $SkipSystemTweaks `
    -NoElevate $NoElevate -KeepFiles $KeepFiles

$global:LASTEXITCODE = $code

# Only terminate the host when we are a real script file. Under `irm | iex`
# there is no $PSCommandPath, and calling exit would close the user's console.
if ($PSCommandPath) { exit $code }
