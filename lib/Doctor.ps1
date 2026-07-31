# Doctor.ps1 - diagnostics and the end-to-end smoke test.
#
# The guide's troubleshooting chapter runs from p.20 to p.25. Most of what it
# asks you to check by hand is mechanically checkable, so Invoke-MadLyxDoctor
# checks it and prints the fix.

<#
    Compile the bundled Hebrew test document to PDF.

    This is the only check that proves the whole chain works: LyX, the TeX
    distribution, babel-hebrew and culmus all have to cooperate. On failure we
    surface the actual LaTeX error rather than "something went wrong".
#>
function Invoke-SmokeTest {
    param(
        [Parameter(Mandatory)]$LyX,
        [Parameter(Mandatory)][string]$PayloadRoot
    )

    Write-Step 'Compiling a Hebrew test document'

    $source = Join-Path $PayloadRoot 'smoketest\smoketest.lyx'
    if (-not (Test-Path $source)) { Write-Warn 'Test document missing - skipping.'; return $null }

    # Deliberately an ASCII-only working directory: a Hebrew path here would be
    # testing the wrong thing.
    $work = Join-Path $env:TEMP "madlyx-smoketest-$(Get-Random)"
    New-Item -ItemType Directory -Force $work | Out-Null
    $lyxFile = Join-Path $work 'smoketest.lyx'
    Copy-Item $source $lyxFile -Force

    Write-Info 'This can take a few minutes the first time (TeX builds its font cache).'

    $stdout = Join-Path $work 'out.txt'
    $stderr = Join-Path $work 'err.txt'
    $proc = Start-Process -FilePath $LyX.Exe `
        -ArgumentList @('-E', 'pdf2', (Join-Path $work 'smoketest.pdf'), $lyxFile) `
        -NoNewWindow -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr

    $completed = $proc.WaitForExit(900 * 1000)
    if (-not $completed) {
        try { $proc.Kill() } catch {}
        Write-Err 'The test compile timed out after 15 minutes.'
        Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue
        return $false
    }

    $pdf = Join-Path $work 'smoketest.pdf'
    $log = @()
    foreach ($f in @($stdout, $stderr)) {
        $text = Get-Content $f -Raw -ErrorAction SilentlyContinue
        if ($text) { $log += $text; Write-Log $text }
    }

    if (Test-Path $pdf) {
        $size = [math]::Round((Get-Item $pdf).Length / 1KB, 1)
        Write-Ok "Hebrew PDF produced successfully ($size KB). Your installation works."
        Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue
        return $true
    }

    Write-Err 'The test document did not compile.'
    $combined = ($log -join "`n")

    # Map the failures the guide documents onto their fixes.
    if ($combined -match "File ['\`"]?culmus\.sty") {
        Write-Info 'Cause: the culmus package is missing (guide, p.21).'
        Write-Info 'Fix:   re-run this installer and accept the Culmus step,'
        Write-Info '       or remove \usepackage{culmus} from the document preamble.'
    }
    elseif ($combined -match "File ['\`"]?cp1255\.def") {
        Write-Info 'Cause: babel-hebrew is missing (guide, p.25).'
        Write-Info 'Fix:   install the babel-hebrew package in MiKTeX Console or via tlmgr.'
    }
    elseif ($combined -match "File ['\`"]?([\w\-]+)\.sty['\`"]? not found") {
        Write-Info "Cause: the LaTeX package '$($Matches[1])' is missing."
        Write-Info "Fix:   install '$($Matches[1])' in MiKTeX Console > Packages, or 'tlmgr install $($Matches[1])'."
    }
    elseif ($combined -match 'Font .* not found') {
        Write-Info 'Cause: a Hebrew font is not registered with TeX (guide, pp.23-24).'
        Write-Info 'Fix:   re-run this installer to re-register the Culmus font maps.'
    }
    else {
        $tail = ($combined -split "`n" | Where-Object { $_.Trim() } | Select-Object -Last 12) -join "`n      "
        Write-Info "Last output from the compile:`n      $tail"
    }

    Write-Info "Full log: $script:MadLyxLogFile"
    Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue
    return $false
}

<#
    Check the installation and report anything that looks wrong, with the fix.
    Safe to run at any time and changes nothing.
#>
function Invoke-MadLyxDoctor {
    param([string]$PayloadRoot)

    Write-Host ''
    Write-Host 'MadLyX doctor' -ForegroundColor Cyan
    Write-Host '=============' -ForegroundColor Cyan

    $problems = New-Object System.Collections.Generic.List[string]

    # --- Paths ---------------------------------------------------------------
    Write-Step 'Paths'
    if (Test-HasHebrew $env:USERPROFILE) {
        Write-Err "Your user folder contains Hebrew: $env:USERPROFILE"
        Write-Info 'This breaks PDF export. There is no supported way to rename a Windows'
        Write-Info 'user folder - create a new account with an English name, or keep all'
        Write-Info 'LyX documents outside your user folder (e.g. C:\Studies).'
        $problems.Add('Hebrew characters in the user profile path')
    } else {
        Write-Ok "User profile path is clean: $env:USERPROFILE"
    }

    # --- LyX -----------------------------------------------------------------
    Write-Step 'LyX'
    $lyx = Find-LyXInstallation
    if (-not $lyx) {
        Write-Err 'LyX is not installed.'
        $problems.Add('LyX not installed')
    } else {
        Write-Ok "LyX $($lyx.Version) at $($lyx.Root)"
        $userDir = Find-LyXUserDir -LyX $lyx
        if (-not $userDir) {
            Write-Err 'LyX settings folder not found - open and close LyX once.'
            $problems.Add('LyX user directory missing')
        } else {
            Write-Ok "Settings folder: $userDir"

            # --- preferences ---------------------------------------------------
            $prefFile = Join-Path $userDir 'preferences'
            if (Test-Path $prefFile) {
                $prefs = Get-Content $prefFile -Raw -Encoding UTF8
                $expected = @{
                    'kbmap true'            = 'Hebrew keyboard map is off - you cannot type Hebrew with F12'
                    'kbmap_secondary "hebr' = 'Hebrew keyboard map is not set as the secondary layout'
                    'visual_cursor true'    = 'Visual (RTL) cursor movement is off - arrow keys will feel wrong in Hebrew'
                    'path_prefix'           = 'LyX has no path to the TeX binaries - PDF export will fail'
                }
                foreach ($needle in $expected.Keys) {
                    if ($prefs -match [regex]::Escape($needle)) {
                        Write-Ok "preferences: $needle"
                    } else {
                        Write-Warn "preferences: missing '$needle' - $($expected[$needle])"
                        $problems.Add($expected[$needle])
                    }
                }
            } else {
                Write-Err 'No preferences file - open and close LyX once.'
                $problems.Add('preferences file missing')
            }

            # --- shortcuts -----------------------------------------------------
            $bindFile = Join-Path $userDir 'bind\user.bind'
            if (Test-Path $bindFile) {
                $bindCount = @(Select-String -Path $bindFile -Pattern '^\\bind').Count
                $header = @(Get-Content $bindFile -TotalCount 5)
                $format = (@($header | Where-Object { $_ -match '^Format\s+(\d+)' }) -join '') -replace '\D', ''
                Write-Ok "Shortcut file present: $bindCount shortcuts (bind Format $format)"

                # bind Format 4 belongs to LyX 2.3, Format 5 to LyX 2.4 and later.
                # LyX converts a mismatched format on load, so this is only worth
                # flagging when the file is for an older series than we would pick.
                $choice = Get-MadLyxBindSeries -LyX $lyx
                $expectedFormat = if ($choice.Series -eq '2.4') { '5' } else { '4' }
                if ($format -and $format -ne $expectedFormat) {
                    Write-Warn "This shortcut file is Format $format; Format $expectedFormat suits LyX $($lyx.Version)."
                    Write-Info 'Re-run the installer to drop in the better-matching file.'
                    $problems.Add('Shortcut file does not match the installed LyX version')
                } elseif (-not $choice.Exact) {
                    Write-Info "LyX $($lyx.Version) has no dedicated MadLyX build; the $($choice.Series) file is in use and LyX converts it on load."
                }
                if ($bindCount -lt 100) {
                    Write-Warn 'The shortcut file looks much smaller than the MadLyX set (~500 shortcuts).'
                }
            } else {
                Write-Warn 'No user.bind - the MadLyX shortcuts are not installed.'
                $problems.Add('MadLyX shortcuts not installed')
            }

            # --- templates -----------------------------------------------------
            $templateDir = Join-Path $userDir 'templates'
            if (Test-Path $templateDir) {
                $count = @(Get-ChildItem $templateDir -Filter '*.lyx' -ErrorAction SilentlyContinue).Count
                Write-Ok "$count template(s) installed."
            } else {
                Write-Warn 'No templates folder.'
            }
        }
    }

    # --- TeX -----------------------------------------------------------------
    Write-Step 'TeX distribution'
    $tex = Find-MiKTeX
    if (-not $tex) { $tex = Find-TeXLive }
    if (-not $tex) {
        Write-Err 'No TeX distribution found - PDF export cannot work.'
        $problems.Add('No TeX distribution installed')
    } else {
        Write-Ok "$($tex.Distro) at $($tex.BinDir)"
        $kpsewhich = Join-Path $tex.BinDir 'kpsewhich.exe'
        if (Test-Path $kpsewhich) {
            foreach ($file in @('culmus.sty', 'cp1255.def', 'preview.sty', 'mathtools.sty')) {
                $found = & $kpsewhich $file 2>$null
                if ($found) { Write-Ok "$file found" }
                else {
                    Write-Warn "$file not found"
                    if ($tex.Distro -eq 'miktex') { Write-Info "  MiKTeX will fetch it on first use, or install it in MiKTeX Console." }
                    else { Write-Info "  Run: tlmgr install $([IO.Path]::GetFileNameWithoutExtension($file))" }
                }
            }
        }
    }

    # --- Windows keyboard ----------------------------------------------------
    Write-Step 'Windows keyboard'
    $toggleKey = 'HKCU:\Keyboard Layout\Toggle'
    $langHotkey = $null
    if (Test-Path $toggleKey) {
        $props = @(Get-ItemProperty $toggleKey -ErrorAction SilentlyContinue)
        if ($props.Count -gt 0) { $langHotkey = $props[0].'Language Hotkey' }
    }
    if ($langHotkey -eq '3') {
        Write-Ok 'Alt+Shift language switching is disabled.'
    } else {
        Write-Warn 'Alt+Shift still switches the Windows input language.'
        Write-Info 'If Windows flips to Hebrew, every LyX shortcut stops working.'
        Write-Info 'Re-run the installer and accept the keyboard step to disable it.'
    }

    # --- Summary -------------------------------------------------------------
    Write-Host ''
    if ($problems.Count -eq 0) {
        Write-Host 'No problems found.' -ForegroundColor Green
    } else {
        Write-Host "$($problems.Count) problem(s) found:" -ForegroundColor Yellow
        foreach ($p in $problems) { Write-Host "  - $p" -ForegroundColor Yellow }
    }
    Write-Host ''
    return $problems.Count
}
