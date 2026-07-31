# Common.ps1 - logging, prompting, backups and small shared helpers.

# Initialised here rather than left to the caller. Everything runs under
# Set-StrictMode -Version Latest, where reading a variable that was never
# assigned is a terminating error - so a module dot-sourced on its own, or
# ahead of install.ps1 setting these, would throw instead of using a default.
$script:MadLyxLogFile    = $null
$script:MadLyxUnattended = $false
$script:MadLyxPromptWorks = $null

function Initialize-MadLyxLog {
    param([string]$Path)
    $script:MadLyxLogFile = $Path
    $dir = Split-Path $Path
    if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Force $dir | Out-Null }
    "=== MadLyX install $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss') ===" | Set-Content $Path -Encoding UTF8
}

function Write-Log {
    param([string]$Message, [string]$Level = 'INFO')
    if ($script:MadLyxLogFile) {
        "[{0}] {1,-5} {2}" -f (Get-Date -Format 'HH:mm:ss'), $Level, $Message |
            Add-Content $script:MadLyxLogFile -Encoding UTF8
    }
}

function Write-Step {
    param([string]$Message)
    Write-Host ''
    Write-Host "==> $Message" -ForegroundColor Cyan
    Write-Log $Message 'STEP'
}

function Write-Info {
    param([string]$Message)
    Write-Host "    $Message" -ForegroundColor Gray
    Write-Log $Message
}

function Write-Ok {
    param([string]$Message)
    Write-Host "    [ok] $Message" -ForegroundColor Green
    Write-Log $Message 'OK'
}

function Write-Warn {
    param([string]$Message)
    Write-Host "    [!]  $Message" -ForegroundColor Yellow
    Write-Log $Message 'WARN'
}

function Write-Err {
    param([string]$Message)
    Write-Host "    [X]  $Message" -ForegroundColor Red
    Write-Log $Message 'ERROR'
}

<#
    Can we actually ask the user something?

    False when -Unattended was passed, or when standard input is redirected -
    a piped invocation, a CI job, or `irm | iex` run through anything other
    than a live console. In those cases Read-Host returns $null rather than
    blocking, and calling .Trim() on it throws.

    The probe result is cached: the answer cannot change mid-run.
#>
function Test-CanPrompt {
    if ($script:MadLyxUnattended) { return $false }
    if ($null -ne $script:MadLyxPromptWorks) { return $script:MadLyxPromptWorks }
    try {
        $script:MadLyxPromptWorks = -not [Console]::IsInputRedirected
    } catch {
        $script:MadLyxPromptWorks = $false
    }
    if (-not $script:MadLyxPromptWorks) {
        Write-Log 'stdin is redirected; questions will use their default answers'
    }
    return $script:MadLyxPromptWorks
}

<#
    Ask the user a yes/no question.

    Returns $Default without asking when prompting is impossible, so the whole
    installer stays scriptable. System-level changes pass -Default:$false so an
    unattended or piped run never silently edits the registry or Defender config.
#>
function Confirm-Action {
    param(
        [Parameter(Mandatory)][string]$Question,
        [string]$Detail,
        [bool]$Default = $true
    )
    if (-not (Test-CanPrompt)) {
        $shown = if ($Default) { 'yes' } else { 'no' }
        Write-Log "auto-answer '$Question' -> $Default (cannot prompt)"
        Write-Host "  ? $Question" -ForegroundColor White
        Write-Host "    (not running interactively - using the default: $shown)" -ForegroundColor DarkGray
        return $Default
    }

    Write-Host ''
    Write-Host "  ? $Question" -ForegroundColor White
    if ($Detail) { foreach ($line in $Detail -split "`n") { Write-Host "    $line" -ForegroundColor DarkGray } }
    $hint = if ($Default) { '[Y/n]' } else { '[y/N]' }

    while ($true) {
        $raw = $null
        try { $raw = Read-Host "    $hint" } catch { $raw = $null }

        # Read-Host yields $null when input ends (Ctrl+Z, closed pipe). Do not
        # loop on it - that would spin forever - and never call .Trim() on it.
        if ($null -eq $raw) {
            Write-Log "input ended while asking '$Question'; using default $Default"
            Write-Host "    (no more input - using the default)" -ForegroundColor DarkGray
            return $Default
        }

        $answer = $raw.Trim().ToLower()
        if ($answer -eq '')           { Write-Log "answered '$Question' -> default $Default"; return $Default }
        if ($answer -in @('y','yes')) { Write-Log "answered '$Question' -> yes"; return $true }
        if ($answer -in @('n','no'))  { Write-Log "answered '$Question' -> no";  return $false }
        Write-Host '    Please answer y or n.' -ForegroundColor DarkGray
    }
}

<#
    Copy a file to <name>.madlyx-backup-<timestamp><ext> before we modify it.
    Returns the backup path, or $null when there was nothing to back up.

    The timestamp only has one-second resolution, so two runs in the same second
    would collide. A backup must never overwrite another backup, so on collision
    we add a counter rather than passing -Force.
#>
function Backup-File {
    param([Parameter(Mandatory)][string]$Path)
    if (-not (Test-Path $Path)) { return $null }

    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $dir   = Split-Path $Path
    $name  = [IO.Path]::GetFileNameWithoutExtension($Path)
    $ext   = [IO.Path]::GetExtension($Path)

    $backup = Join-Path $dir "$name.madlyx-backup-$stamp$ext"
    $n = 2
    while (Test-Path $backup) {
        $backup = Join-Path $dir "$name.madlyx-backup-$stamp-$n$ext"
        $n++
    }

    Copy-Item $Path $backup
    Write-Log "backed up $Path -> $backup"
    return $backup
}

function Test-IsAdmin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    (New-Object Security.Principal.WindowsPrincipal $id).IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Test-HasHebrew {
    param([string]$Text)
    # U+0590-U+05FF Hebrew, U+FB1D-U+FB4F Hebrew presentation forms.
    # Written as \u escapes so this file stays pure ASCII: Windows PowerShell
    # 5.1 reads a .ps1 with no byte-order mark using the system ANSI codepage,
    # which would mangle literal Hebrew and make this silently detect nothing.
    return $Text -match '[\u0590-\u05FF\uFB1D-\uFB4F]'
}

<#
    Run a native command, capturing output to the log rather than the console.
    Returns $true on exit code 0. Never throws - callers decide what a failure means.
#>
function Invoke-Native {
    param(
        [Parameter(Mandatory)][string]$FilePath,
        [string[]]$Arguments = @(),
        [int]$TimeoutSeconds = 0
    )
    Write-Log "run: $FilePath $($Arguments -join ' ')"
    try {
        $stdout = New-TemporaryFile
        $stderr = New-TemporaryFile
        $p = Start-Process -FilePath $FilePath -ArgumentList $Arguments -NoNewWindow -PassThru `
                           -RedirectStandardOutput $stdout -RedirectStandardError $stderr
        if ($TimeoutSeconds -gt 0) {
            if (-not $p.WaitForExit($TimeoutSeconds * 1000)) {
                Write-Log "timeout after ${TimeoutSeconds}s, killing $FilePath" 'WARN'
                try { $p.Kill() } catch {}
                return $false
            }
        } else {
            $p.WaitForExit()
        }
        foreach ($f in @($stdout, $stderr)) {
            $text = Get-Content $f -Raw -ErrorAction SilentlyContinue
            if ($text) { Write-Log $text.Trim() }
            Remove-Item $f -Force -ErrorAction SilentlyContinue
        }
        Write-Log "exit code: $($p.ExitCode)"
        return ($p.ExitCode -eq 0)
    } catch {
        Write-Log "failed to run ${FilePath}: $($_.Exception.Message)" 'ERROR'
        return $false
    }
}

<#
    Resolve a command to its file path, or $null when it is not on PATH.

    Written defensively because the whole installer runs under
    Set-StrictMode -Version Latest, where reading .Source off an empty
    Get-Command result is a terminating error rather than $null.
#>
function Get-CommandPath {
    param([Parameter(Mandatory)][string]$Name)
    $cmd = @(Get-Command $Name -ErrorAction SilentlyContinue)
    if ($cmd.Count -eq 0) { return $null }
    return $cmd[0].Source
}

<#
    Download a file with a progress-free, reasonably patient web request.
    Returns $true on success.
#>
function Get-RemoteFile {
    param(
        [Parameter(Mandatory)][string]$Uri,
        [Parameter(Mandatory)][string]$OutFile
    )
    $previous = $ProgressPreference
    $ProgressPreference = 'SilentlyContinue'
    try {
        Write-Log "download: $Uri -> $OutFile"
        Invoke-WebRequest -Uri $Uri -OutFile $OutFile -UseBasicParsing -TimeoutSec 600 -ErrorAction Stop
        return $true
    } catch {
        Write-Log "download failed: $($_.Exception.Message)" 'ERROR'
        return $false
    } finally {
        $ProgressPreference = $previous
    }
}
