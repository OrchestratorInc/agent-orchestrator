param([switch]$Session)

$ErrorActionPreference = 'Continue'

if (-not $Session) {
  # Outer window: size it, run this same script inside the Codex sandbox, then set
  # the final title. The title is what tells capture-window.ps1 the output is complete.
  try {
    $b = $Host.UI.RawUI.BufferSize; $b.Width = 120; $b.Height = 300; $Host.UI.RawUI.BufferSize = $b
    $w = $Host.UI.RawUI.WindowSize; $w.Width = 120; $w.Height = 24; $Host.UI.RawUI.WindowSize = $w
  } catch { }
  $Host.UI.RawUI.WindowTitle = 'running'
  codex sandbox powershell -NoProfile -ExecutionPolicy Bypass -File $PSCommandPath -Session
  $Host.UI.RawUI.WindowTitle = 'codex sandbox - BEFORE - ao pinned from the install directory'
  Start-Sleep -Seconds 600
  return
}

# --- everything below runs as the sandbox identity ---
$data = 'C:\Users\ACER\.ao\dev\pr-7771-evidence'
$install = 'C:\Users\ACER\AppData\Local\Temp\ao-7171\evidence\Programs\AO main\resources\daemon'
$env:AO_DATA_DIR = $data
$env:AO_RUN_FILE = Join-Path $data 'running.json'

Write-Host ''
Write-Host 'BEFORE this PR: the launcher pins ao from the install directory' -ForegroundColor Cyan
Write-Host '--------------------------------------------------------------' -ForegroundColor DarkGray
Write-Host ''
Write-Host 'PS> whoami' -ForegroundColor Green
Write-Host ('    ' + (whoami))
Write-Host ''
Write-Host ('PS> $install = "' + $install + '"') -ForegroundColor Green
Write-Host ''
Write-Host 'PS> icacls $install' -ForegroundColor Green
try { icacls $install 2>&1 | ForEach-Object { Write-Host ('    ' + $_) -ForegroundColor Red } } catch { }
Write-Host ''
Write-Host 'PS> $env:PATH = "$install;$env:PATH"' -ForegroundColor Green
$env:PATH = $install + ';' + $env:PATH
Write-Host 'PS> ao status' -ForegroundColor Green
try {
  ao status 2>&1 | ForEach-Object { Write-Host ('    ' + $_) }
} catch {
  Write-Host ('    ' + $_.Exception.GetType().Name + ': ' + $_.Exception.Message) -ForegroundColor Red
}
Write-Host ''
Write-Host 'PS> Test-Path "$install\ao.exe"' -ForegroundColor Green
try { Write-Host ('    ' + (Test-Path ($install + '\ao.exe') -ErrorAction Stop)) } catch { Write-Host ('    ' + $_.Exception.Message) -ForegroundColor Red }
Write-Host ''
Write-Host 'the sandbox can neither stat nor run ao from there' -ForegroundColor DarkGray
