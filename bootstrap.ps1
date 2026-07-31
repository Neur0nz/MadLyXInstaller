<#
.SYNOPSIS
    One-line web installer for MadLyX.

.DESCRIPTION
    Downloads madlyx.exe from the latest GitHub release and runs it.

    This is the only PowerShell left in the project, and the only piece that
    has to be: it is what a clean Windows machine can run without installing
    anything first. Everything else is a single self-contained binary with the
    payload compiled in, so none of the problems this script used to have -
    execution policy, PowerShell 5.1 versus 7 dialects, ANSI-mangled source -
    apply past this point.

    Safe to pipe into iex: nothing here calls exit at top level, which would
    close the console it is running in.

.EXAMPLE
    irm https://raw.githubusercontent.com/Neur0nz/MadLyXInstaller/main/bootstrap.ps1 | iex

.EXAMPLE
    # With arguments - `irm | iex` cannot take them, so use the scriptblock form
    & ([scriptblock]::Create((irm https://raw.githubusercontent.com/Neur0nz/MadLyXInstaller/main/bootstrap.ps1))) doctor
#>
[CmdletBinding()]
param(
    # Arguments passed straight to madlyx.exe, e.g. doctor, --dry-run.
    #
    # Deliberately not named $Args: that is a reserved automatic variable, and
    # because `iex` executes in the caller's scope, PowerShell refuses to
    # overwrite it - "Cannot overwrite variable Args because the variable has
    # been optimized". Invisible until the script is actually piped into iex.
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$Arguments,

    [string]$Repo = 'Neur0nz/MadLyXInstaller',
    [string]$Version = 'latest',
    [switch]$KeepFiles
)

$ErrorActionPreference = 'Stop'

# Windows PowerShell 5.1 still negotiates TLS 1.0 on some builds; GitHub refuses it.
try {
    [Net.ServicePointManager]::SecurityProtocol =
        [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch { }

function Invoke-MadLyxBootstrap {
    param($Repo, $Version, $Arguments, $KeepFiles)

    Write-Host ''
    Write-Host '  MadLyX' -ForegroundColor Cyan
    Write-Host ''

    $work = Join-Path $env:TEMP "madlyx-$(Get-Date -Format 'yyyyMMdd-HHmmss')"
    New-Item -ItemType Directory -Force $work | Out-Null
    $exe = Join-Path $work 'madlyx.exe'

    try {
        $tag = $Version
        if ($Version -eq 'latest') {
            Write-Host '  Finding the latest release...' -ForegroundColor Gray
            $rel = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest" -TimeoutSec 60
            $tag = $rel.tag_name
        }
        $url = "https://github.com/$Repo/releases/download/$tag/madlyx.exe"

        Write-Host "  Downloading madlyx.exe ($tag)..." -ForegroundColor Gray
        $previous = $ProgressPreference
        $ProgressPreference = 'SilentlyContinue'
        try {
            Invoke-WebRequest -Uri $url -OutFile $exe -UseBasicParsing -TimeoutSec 600
        } finally {
            $ProgressPreference = $previous
        }

        if (-not (Test-Path $exe)) { throw 'the download produced no file' }
        Write-Host ("  Downloaded {0:N1} MB." -f ((Get-Item $exe).Length / 1MB)) -ForegroundColor Gray

        # Invoke-WebRequest does not stamp the mark-of-the-web, so SmartScreen's
        # reputation check cannot fire here. Unblock anyway: it costs nothing and
        # covers a future change to how the file arrives.
        try { Unblock-File $exe } catch { }

        Write-Host ''

        # Deliberately not captured, piped, or returned.
        #
        # PowerShell only redirects a native command's stdout when something
        # consumes it - and `$code = Invoke-MadLyxBootstrap ...` consumed it,
        # because a function returns everything left on its pipeline. That one
        # assignment swallowed every line madlyx.exe printed: no banner, no
        # steps, no summary. Worse, it made stdout a pipe rather than a
        # console, so the installer correctly concluded it was running
        # unattended and stopped asking questions - which is why the elevation
        # prompt never appeared and Defender exclusions silently failed with
        # "needs administrator rights".
        #
        # Left bare, the process inherits the real console: output is visible,
        # colour and the live progress display work, and prompts can be
        # answered. The exit code comes from $LASTEXITCODE, which & sets.
        & $exe @Arguments
    }
    catch {
        Write-Host ''
        Write-Host "  Bootstrap failed: $($_.Exception.Message)" -ForegroundColor Red
        Write-Host ''
        Write-Host '  Install manually instead:' -ForegroundColor Gray
        Write-Host "    1. Download madlyx.exe from https://github.com/$Repo/releases/latest" -ForegroundColor Gray
        Write-Host '    2. Run it from a terminal' -ForegroundColor Gray
        Write-Host ''
        $global:LASTEXITCODE = 1
    }
    finally {
        if ($KeepFiles) {
            Write-Host "  Kept: $work" -ForegroundColor DarkGray
        } else {
            Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

Invoke-MadLyxBootstrap -Repo $Repo -Version $Version -Arguments $Arguments -KeepFiles:$KeepFiles
$code = $LASTEXITCODE
if ($null -eq $code) { $code = 0 }
$global:LASTEXITCODE = $code

# Only terminate the host when running as a real script file. Under `irm | iex`
# there is no $PSCommandPath, and exit would close the user's console.
if ($PSCommandPath) { exit $code }
