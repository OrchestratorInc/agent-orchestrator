param([switch]$Session)

$ErrorActionPreference = 'Continue'

if (-not $Session) {
  # Outer window: size it, run this same script inside the Codex sandbox, then set
  # the final title. The title is what tells capture-window.ps1 the output is complete.
  try {
    $b = $Host.UI.RawUI.BufferSize; $b.Width = 120; $b.Height = 300; $Host.UI.RawUI.BufferSize = $b
    $w = $Host.UI.RawUI.WindowSize; $w.Width = 120; $w.Height = 32; $Host.UI.RawUI.WindowSize = $w
  } catch { }
  $Host.UI.RawUI.WindowTitle = 'running'
  codex sandbox powershell -NoProfile -ExecutionPolicy Bypass -File $PSCommandPath -Session
  $Host.UI.RawUI.WindowTitle = 'codex sandbox - AFTER - ao pinned from the data-directory shim'
  Start-Sleep -Seconds 600
  return
}

# --- everything below runs as the sandbox identity ---
$data = 'C:\Users\ACER\.ao\dev\pr-7771-evidence'
$shim = 'C:\Users\ACER\.ao\dev\pr-7771-evidence\runtime\ao-path\3fd4cbb20f011375973b07c5acfb6b18'
$env:AO_DATA_DIR = $data
$env:AO_RUN_FILE = Join-Path $data 'running.json'

Write-Host ''
Write-Host 'AFTER this PR: the launcher pins ao from the data-directory shim' -ForegroundColor Cyan
Write-Host '---------------------------------------------------------------' -ForegroundColor DarkGray
Write-Host ''
Write-Host 'PS> whoami' -ForegroundColor Green
Write-Host ('    ' + (whoami))
Write-Host ''
Write-Host ('PS> $shim = "' + $shim + '"') -ForegroundColor Green
Write-Host ''
Write-Host 'PS> (Get-Acl $shim).Access | ? IdentityReference -match CodexSandbox' -ForegroundColor Green
(Get-Acl $shim).Access |
  Where-Object { $_.IdentityReference -match 'CodexSandbox' } |
  Format-Table IdentityReference, FileSystemRights, AccessControlType -AutoSize |
  Out-String -Width 118 |
  ForEach-Object { Write-Host ('    ' + $_.TrimEnd()) }
Write-Host ''
Write-Host 'PS> $env:PATH = "$shim;$env:PATH"' -ForegroundColor Green
$env:PATH = $shim + ';' + $env:PATH
Write-Host 'PS> where.exe ao' -ForegroundColor Green
where.exe ao 2>&1 | ForEach-Object { Write-Host ('    ' + $_) }
Write-Host ''
Write-Host 'PS> ao --version' -ForegroundColor Green
Write-Host ('    ' + (& ao --version 2>&1))
Write-Host ''
Write-Host 'PS> ao status' -ForegroundColor Green
& ao status 2>&1 | ForEach-Object { Write-Host ('    ' + $_) }
Write-Host ''
Write-Host 'ao runs inside the sandbox' -ForegroundColor DarkGray
