# Test-Prompting.ps1
#
# Guards the non-interactive paths.
#
# The bug this exists for: Confirm-Action did (Read-Host ...).Trim(), and
# Read-Host returns $null rather than blocking when stdin is redirected. The
# installer died with "You cannot call a method on a null-valued expression"
# the moment it asked anything from a piped session or CI job.

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repo = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$fails = 0
function Check($label, $condition) {
    if ($condition) { Write-Host "  PASS  $label" -ForegroundColor Green; return 0 }
    Write-Host "  FAIL  $label" -ForegroundColor Red; return 1
}

# ---------------------------------------------------------------------------
#  Confirm-Action must never throw, and must honour the default, when stdin
#  is not a console. Run in a child process with redirected input so the
#  condition is genuine rather than simulated.
# ---------------------------------------------------------------------------
Write-Host "`n=== Confirm-Action with redirected stdin ===" -ForegroundColor Cyan

$probe = @"
`$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
. '$repo\lib\Common.ps1'
try {
    `$a = Confirm-Action -Question 'default true?'  -Default `$true
    `$b = Confirm-Action -Question 'default false?' -Default `$false
    'RESULT:' + `$a + ',' + `$b
} catch {
    'THREW:' + `$_.Exception.Message
}
"@

foreach ($exe in @(
    @{ Name = 'PowerShell 7';          Path = (Get-Process -Id $PID).Path },
    @{ Name = 'Windows PowerShell 5.1'; Path = (Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe') }
)) {
    if (-not (Test-Path $exe.Path)) { continue }
    $file = Join-Path $env:TEMP "prompt-probe-$(Get-Random).ps1"
    Set-Content $file $probe -Encoding UTF8
    # Piping input is what makes [Console]::IsInputRedirected true.
    $out = '' | & $exe.Path -NoProfile -ExecutionPolicy Bypass -File $file 2>&1 | Out-String
    Remove-Item $file -Force -ErrorAction SilentlyContinue

    $fails += Check "$($exe.Name): Confirm-Action does not throw" ($out -notmatch 'THREW:')
    $fails += Check "$($exe.Name): honours -Default true"  ($out -match 'RESULT:True,')
    $fails += Check "$($exe.Name): honours -Default false" ($out -match ',False')
}

# ---------------------------------------------------------------------------
#  System-level changes must default to "no" so a piped run never silently
#  edits the registry or Defender configuration.
# ---------------------------------------------------------------------------
Write-Host "`n=== system-level changes default to no ===" -ForegroundColor Cyan
foreach ($fn in @('Disable-LanguageToggleHotkey', 'Add-DefenderExclusions')) {
    $src = Get-Content "$repo\lib\SystemTweaks.ps1" -Raw
    $body = [regex]::Match($src, "function $fn \{.*?\n\}", 'Singleline').Value
    $hasConfirm = $body -match 'Confirm-Action'
    $defaultsFalse = $body -match '-Default \$false'
    $fails += Check "$fn asks before acting" $hasConfirm
    $fails += Check "$fn defaults to no"     $defaultsFalse
}

# ---------------------------------------------------------------------------
#  Elevation must not be attempted without a console to accept the UAC dialog.
# ---------------------------------------------------------------------------
Write-Host "`n=== elevation is gated on interactivity ===" -ForegroundColor Cyan
$install = Get-Content "$repo\install.ps1" -Raw
$fails += Check 'elevation block checks Test-CanPrompt' ($install -match 'Test-IsAdmin\)[^\n]*Test-CanPrompt|Test-CanPrompt[^\n]*\)\s*\{')

# ---------------------------------------------------------------------------
#  No unguarded Read-Host anywhere.
# ---------------------------------------------------------------------------
Write-Host "`n=== no unguarded Read-Host ===" -ForegroundColor Cyan
foreach ($f in @(Get-ChildItem "$repo\lib" -Filter *.ps1) + @(Get-Item "$repo\install.ps1")) {
    $lines = Get-Content $f.FullName
    $bad = @()
    $inBlockComment = $false
    for ($i = 0; $i -lt $lines.Count; $i++) {
        # Track <# ... #> so prose describing Read-Host is not mistaken for a call.
        if ($lines[$i] -match '<#') { $inBlockComment = $true }
        if ($inBlockComment) {
            if ($lines[$i] -match '#>') { $inBlockComment = $false }
            continue
        }
        if ($lines[$i] -notmatch 'Read-Host') { continue }
        if ($lines[$i] -match '^\s*#') { continue }
        # Acceptable: inside Confirm-Action's own guarded loop, or wrapped in try.
        $context = ($lines[[Math]::Max(0, $i - 3)..$i] -join ' ')
        if ($context -match 'try\s*\{|\$raw\s*=') { continue }
        $bad += "line $($i + 1)"
    }
    $fails += Check "$($f.Name): Read-Host calls are guarded ($(if ($bad) { $bad -join ', ' } else { 'all clear' }))" ($bad.Count -eq 0)
}

Write-Host ''
if ($fails -eq 0) { Write-Host 'ALL CHECKS PASSED' -ForegroundColor Green }
else { Write-Host "$fails CHECK(S) FAILED" -ForegroundColor Red }
exit $fails
