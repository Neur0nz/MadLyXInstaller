# Exercises the config layer without needing LyX installed.
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repo = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
foreach ($m in 'Common','Preflight','LyX','TeX','Configure','SystemTweaks','Doctor') { . "$repo\lib\$m.ps1" }

$script:MadLyxUnattended = $true
$sandbox = Join-Path $env:TEMP "madlyx-test-$(Get-Random)"
$userDir = Join-Path $sandbox 'LyX2.4'
New-Item -ItemType Directory -Force $userDir | Out-Null
Initialize-MadLyxLog (Join-Path $sandbox 'test.log')

# A realistic pre-existing preferences file, including keys we manage
# (to prove they get replaced, not duplicated) and keys we don't (to prove
# they survive untouched).
@'
# LyX 2.4.4 generated this file. If you want to make your own
Format 36

\gui_language hebrew
\kbmap false
\screen_font_roman "DejaVu Serif"
\visual_cursor false
\set_color "green" "#00ff00"
\user_name "Test User"
'@ | Set-Content (Join-Path $userDir 'preferences') -Encoding UTF8

$fakeTex = [pscustomobject]@{ Distro='miktex'; BinDir='C:\Program Files\MiKTeX\miktex\bin\x64' }
$fakeLyX = [pscustomobject]@{ Root='C:\Program Files\LyX 2.4'; Exe='C:\Program Files\LyX 2.4\bin\LyX.exe'
                              Version=[version]'2.4.4'; Series='2.4'; UserDirName='LyX2.4' }

function Check($label, $condition) {
    if ($condition) { Write-Host "  PASS  $label" -ForegroundColor Green; return 0 }
    Write-Host "  FAIL  $label" -ForegroundColor Red; return 1
}
$fails = 0

Write-Host "`n--- run 1 ---" -ForegroundColor Cyan
$s = Get-MadLyxSettings -Tex $fakeTex -LyX $fakeLyX -UserDir $userDir
$c = Get-MadLyxEditorColors -LyX $fakeLyX
Set-LyXPreferences -UserDir $userDir -Settings $s -Colors $c

Write-Host "`n--- run 2 (idempotency) ---" -ForegroundColor Cyan
Set-LyXPreferences -UserDir $userDir -Settings $s -Colors $c

Write-Host "`n--- run 3 (idempotency) ---" -ForegroundColor Cyan
Set-LyXPreferences -UserDir $userDir -Settings $s -Colors $c

$lines = Get-Content (Join-Path $userDir 'preferences')

Write-Host "`n=== assertions ===" -ForegroundColor Cyan
foreach ($key in @('gui_language','kbmap','kbmap_primary','kbmap_secondary','visual_cursor',
                   'scroll_below_document','bind_file','path_prefix','preview','autosave')) {
    $n = @($lines | Where-Object { $_ -match "^\\$key(\s|$)" }).Count
    $fails += Check "exactly one \$key (found $n)" ($n -eq 1)
}
$n = @($lines | Where-Object { $_ -match '^\\set_color "green"' }).Count
$fails += Check "exactly one \set_color green (found $n)" ($n -eq 1)
$fails += Check 'stale green #00ff00 was replaced' (-not ($lines -match '#00ff00'))
$fails += Check 'muted green #b5bd68 present' ([bool]($lines -match 'b5bd68'))
$fails += Check 'unmanaged \user_name survived' ([bool]($lines -match '^\\user_name'))
$fails += Check 'unmanaged \screen_font_roman survived' ([bool]($lines -match '^\\screen_font_roman'))
$fails += Check 'Format header survived' ([bool]($lines -match '^Format 36'))
$fails += Check 'exactly one BEGIN marker' (@($lines | Where-Object { $_ -like '*BEGIN MadLyXInstaller*' }).Count -eq 1)
$fails += Check 'exactly one END marker'   (@($lines | Where-Object { $_ -like '*END MadLyXInstaller*' }).Count -eq 1)
$fails += Check 'gui_language now english' ([bool]($lines -match '^\\gui_language english'))
$fails += Check 'kbmap now true'           ([bool]($lines -match '^\\kbmap true'))
$fails += Check 'path uses forward slashes' ([bool]($lines -match 'path_prefix "C:/Program Files/MiKTeX'))
$fails += Check 'backups created (3 runs)' (@(Get-ChildItem $userDir -Filter '*madlyx-backup*').Count -eq 3)
# -AsByteStream is PowerShell 7 only and -Encoding Byte is 5.1 only; .NET works on both.
$headBytes = [IO.File]::ReadAllBytes((Join-Path $userDir 'preferences'))
$hasBom = $headBytes.Length -ge 3 -and $headBytes[0] -eq 0xEF -and $headBytes[1] -eq 0xBB -and $headBytes[2] -eq 0xBF
$fails += Check 'no BOM' (-not $hasBom)

# 2.3 must get no colour overrides
$lyx23 = [pscustomobject]@{ Root='x'; Exe='x'; Version=[version]'2.3.7'; Series='2.3'; UserDirName='LyX2.3' }
$fails += Check 'LyX 2.3 gets no colour overrides' ((Get-MadLyxEditorColors -LyX $lyx23).Count -eq 0)

# Bind file selection must match the LyX series
Install-MadLyxBindFile -UserDir $userDir -LyX $fakeLyX -PayloadRoot "$repo\payload" | Out-Null
$fmt = (Get-Content (Join-Path $userDir 'bind\user.bind') -TotalCount 5 | Where-Object { $_ -match '^Format' })
$fails += Check "LyX 2.4 got bind Format 5 (got '$fmt')" ($fmt -match 'Format 5')

Install-MadLyxBindFile -UserDir $userDir -LyX $lyx23 -PayloadRoot "$repo\payload" | Out-Null
$fmt = (Get-Content (Join-Path $userDir 'bind\user.bind') -TotalCount 5 | Where-Object { $_ -match '^Format' })
$fails += Check "LyX 2.3 got bind Format 4 (got '$fmt')" ($fmt -match 'Format 4')

Install-MadLyxTemplates -UserDir $userDir -PayloadRoot "$repo\payload" | Out-Null
$tpl = @(Get-ChildItem (Join-Path $userDir 'templates') -Filter '*.lyx')
$fails += Check "5 templates + defaults.lyx installed (got $($tpl.Count))" ($tpl.Count -eq 6)
$fails += Check 'defaults.lyx is the Hebrew Article' ((Get-Content (Join-Path $userDir 'templates\defaults.lyx') -Raw) -match 'heb-article')

Write-Host ''
if ($fails -eq 0) { Write-Host "ALL CHECKS PASSED" -ForegroundColor Green }
else { Write-Host "$fails CHECK(S) FAILED" -ForegroundColor Red }
Write-Host "sandbox: $sandbox" -ForegroundColor DarkGray
exit $fails
