# Test-Bootstrap.ps1
#
# Structural checks on bootstrap.ps1. The bug this exists to prevent: adding a
# parameter to install.ps1 and forgetting to forward it from bootstrap.ps1, so
# the documented one-liner rejects a flag that works from a clone.

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repo = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$fails = 0
function Check($label, $condition) {
    if ($condition) { Write-Host "  PASS  $label" -ForegroundColor Green; return 0 }
    Write-Host "  FAIL  $label" -ForegroundColor Red; return 1
}

function Get-ScriptParameters([string]$Path) {
    $tokens = $null; $errors = $null
    $ast = [System.Management.Automation.Language.Parser]::ParseFile($Path, [ref]$tokens, [ref]$errors)
    if ($errors -and $errors.Count) { throw "parse errors in $Path" }
    $block = $ast.ParamBlock
    if (-not $block) { return @() }
    return @($block.Parameters | ForEach-Object { $_.Name.VariablePath.UserPath })
}

Write-Host "`n=== parameter parity: install.ps1 -> bootstrap.ps1 ===" -ForegroundColor Cyan
$installParams   = Get-ScriptParameters "$repo\install.ps1"
$bootstrapParams = Get-ScriptParameters "$repo\bootstrap.ps1"
Write-Host "  install.ps1:   $($installParams -join ', ')" -ForegroundColor DarkGray
Write-Host "  bootstrap.ps1: $($bootstrapParams -join ', ')" -ForegroundColor DarkGray

foreach ($p in $installParams) {
    $fails += Check "bootstrap accepts -$p" ($bootstrapParams -contains $p)
}

# Accepting the parameter is not enough - it has to actually be forwarded.
$src = Get-Content "$repo\bootstrap.ps1" -Raw
foreach ($p in $installParams) {
    if ($p -eq 'TeXDistribution') {
        $fails += Check "bootstrap forwards -$p" ($src -match "forward\['TeXDistribution'\]")
        continue
    }
    $fails += Check "bootstrap forwards -$p" ($src -match "forward\['$p'\]")
}

# And passed into the inner function, or it is always $null inside.
$innerParam = [regex]::Match($src, 'function Invoke-MadLyxBootstrap\s*\{\s*param\(([^)]*)\)', 'Singleline')
foreach ($p in $installParams) {
    $fails += Check "inner function receives -$p" ($innerParam.Groups[1].Value -match "\`$$p\b")
}

Write-Host "`n=== safety properties ===" -ForegroundColor Cyan
# A bare `exit` at top level would close the console of anyone using irm | iex.
$topLevelExit = $src -match '(?m)^\s*exit\s' -and $src -notmatch 'if \(\$PSCommandPath\) \{ exit'
$fails += Check 'no unguarded top-level exit (would close the user console)' (-not $topLevelExit)
$fails += Check 'exit is guarded by $PSCommandPath' ($src -match 'if \(\$PSCommandPath\) \{ exit')
$fails += Check 'forces TLS 1.2 for Windows PowerShell 5.1' ($src -match 'Tls12')
$fails += Check 'unblocks downloaded scripts (mark-of-the-web)' ($src -match 'Unblock-File')
$fails += Check 'cleans up the temp folder unless -KeepFiles' ($src -match 'Remove-Item \$work -Recurse')
$fails += Check 'refuses to run with the OWNER placeholder' ($src -match "\`$Repo -like 'OWNER/\*'")
$fails += Check 'prints a manual-install fallback on failure' ($src -match 'You can install manually instead')

Write-Host "`n=== bootstrap is genuinely self-contained ===" -ForegroundColor Cyan
# It must not depend on lib\ or payload\, because under iex neither exists.
# Checked against the AST, not the text: the doc comment legitimately mentions
# that install.ps1 dot-sources lib\, and a text search would match that.
$tokens = $null; $errors = $null
$ast = [System.Management.Automation.Language.Parser]::ParseFile(
    "$repo\bootstrap.ps1", [ref]$tokens, [ref]$errors)

$dotSourced = @($ast.FindAll({
    param($n)
    $n -is [System.Management.Automation.Language.CommandAst] -and
    $n.InvocationOperator -eq [System.Management.Automation.Language.TokenKind]::Dot
}, $true))
$fails += Check "no dot-sourcing at all (found $($dotSourced.Count))" ($dotSourced.Count -eq 0)

# $PSScriptRoot is empty under iex, so relying on it would break the one-liner.
$scriptRootUse = @($ast.FindAll({
    param($n)
    $n -is [System.Management.Automation.Language.VariableExpressionAst] -and
    $n.VariablePath.UserPath -in @('PSScriptRoot', 'PSCommandPath')
}, $true) | Where-Object { $_.VariablePath.UserPath -eq 'PSScriptRoot' })
$fails += Check 'does not depend on $PSScriptRoot' ($scriptRootUse.Count -eq 0)

Write-Host ''
if ($fails -eq 0) { Write-Host 'ALL CHECKS PASSED' -ForegroundColor Green }
else { Write-Host "$fails CHECK(S) FAILED" -ForegroundColor Red }
exit $fails
