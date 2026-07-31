# Test-Compat.ps1
#
# Windows PowerShell 5.1 compatibility guard.
#
# The target audience runs whatever `powershell.exe` gives them, which is
# Windows PowerShell 5.1 on every stock Windows machine. Developing in
# PowerShell 7 makes it very easy to write code that parses fine locally and
# explodes for the user - which is exactly what happened:
#
#   * `??` in LyX.ps1 made the installer unloadable under 5.1.
#   * Literal Hebrew in Common.ps1 with no byte-order mark was silently
#     mangled by 5.1's ANSI reading, quietly breaking Hebrew path detection.
#
# This suite fails on both classes of mistake.

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repo = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$fails = 0
function Check($label, $condition) {
    if ($condition) { Write-Host "  PASS  $label" -ForegroundColor Green; return 0 }
    Write-Host "  FAIL  $label" -ForegroundColor Red; return 1
}

$scripts = @(Get-ChildItem $repo -Recurse -Filter *.ps1 |
             Where-Object { $_.FullName -notmatch '\\\.git\\' })

# ---------------------------------------------------------------------------
#  Every script must parse under Windows PowerShell 5.1
# ---------------------------------------------------------------------------
Write-Host "`n=== parses under Windows PowerShell 5.1 ===" -ForegroundColor Cyan
$ps51 = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
if (-not (Test-Path $ps51)) {
    Write-Host '  SKIP  Windows PowerShell 5.1 not present on this machine' -ForegroundColor Yellow
} else {
    foreach ($s in $scripts) {
        $errText = & $ps51 -NoProfile -Command @"
`$t = `$null; `$e = `$null
[System.Management.Automation.Language.Parser]::ParseFile('$($s.FullName)', [ref]`$t, [ref]`$e) | Out-Null
if (`$e -and `$e.Count) { 'line ' + `$e[0].Extent.StartLineNumber + ': ' + `$e[0].Message }
"@ 2>&1
        $ok = -not $errText
        $fails += Check "$($s.Name) parses under 5.1" $ok
        if (-not $ok) { Write-Host "        $errText" -ForegroundColor DarkRed }
    }
}

# ---------------------------------------------------------------------------
#  No PowerShell 7-only syntax
# ---------------------------------------------------------------------------
Write-Host "`n=== no PowerShell 7-only constructs ===" -ForegroundColor Cyan
$banned = @(
    @{ Name = 'null-coalescing ??';        Pattern = '\?\?' }
    @{ Name = 'null-conditional ?.';       Pattern = '\$\w+\?\.' }
    @{ Name = 'ternary ? :';               Pattern = '\)\s*\?\s*[^\s].*\s:\s' }
    @{ Name = '-AsByteStream';             Pattern = '-AsByteStream' }
    @{ Name = 'ForEach-Object -Parallel';  Pattern = '-Parallel\b' }
    @{ Name = 'ConvertFrom-Json -AsHashtable'; Pattern = '-AsHashtable' }
    @{ Name = '$PSStyle';                  Pattern = '\$PSStyle' }
    @{ Name = 'Split-Path -LeafBase';      Pattern = '-LeafBase' }
)
# This file lists the banned patterns as data, so scanning it would always match.
$scanTargets = @($scripts | Where-Object { $_.Name -ne 'Test-Compat.ps1' })
foreach ($b in $banned) {
    $hits = @()
    foreach ($s in $scanTargets) {
        # Strip comments so prose describing these constructs does not trip the check.
        $code = (Get-Content $s.FullName) |
                Where-Object { $_ -notmatch '^\s*#' } |
                ForEach-Object { $_ -replace '\s+#.*$', '' }
        if (($code -join "`n") -match $b.Pattern) { $hits += $s.Name }
    }
    $fails += Check "no $($b.Name) (found in: $(if ($hits) { $hits -join ', ' } else { 'none' }))" ($hits.Count -eq 0)
}

# ---------------------------------------------------------------------------
#  Source must be pure ASCII, or carry a BOM
# ---------------------------------------------------------------------------
Write-Host "`n=== encoding safety ===" -ForegroundColor Cyan
foreach ($s in $scripts) {
    $bytes = [IO.File]::ReadAllBytes($s.FullName)
    $hasBom = $bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF
    $nonAscii = @($bytes | Where-Object { $_ -gt 127 }).Count
    # Non-ASCII with no BOM is read as ANSI by 5.1 and silently corrupted.
    $safe = ($nonAscii -eq 0) -or $hasBom
    $fails += Check "$($s.Name) is ASCII-only or has a BOM (non-ascii bytes: $nonAscii, bom: $hasBom)" $safe
}

# ---------------------------------------------------------------------------
#  Hebrew detection must survive being loaded by 5.1
# ---------------------------------------------------------------------------
Write-Host "`n=== Hebrew detection works under 5.1 ===" -ForegroundColor Cyan
if (Test-Path $ps51) {
    $result = & $ps51 -NoProfile -Command @"
. '$repo\lib\Common.ps1'
`$heb = [string][char]0x05E0 + [char]0x05D3 + [char]0x05D1
if (Test-HasHebrew ('C:\Users\' + `$heb)) { 'DETECTED' } else { 'MISSED' }
if (Test-HasHebrew 'C:\Users\nadav') { 'FALSE-POSITIVE' } else { 'CLEAN' }
"@ 2>&1
    $fails += Check 'detects Hebrew when loaded by 5.1'    ($result -contains 'DETECTED')
    $fails += Check 'no false positive on ASCII under 5.1' ($result -contains 'CLEAN')
}

Write-Host ''
if ($fails -eq 0) { Write-Host 'ALL CHECKS PASSED' -ForegroundColor Green }
else { Write-Host "$fails CHECK(S) FAILED" -ForegroundColor Red }
exit $fails
