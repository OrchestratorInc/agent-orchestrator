param(
  [Parameter(Mandatory=$true)][string]$Wrapper,
  [Parameter(Mandatory=$true)][string]$Title,
  [Parameter(Mandatory=$true)][string]$Out
)

Add-Type -AssemblyName System.Drawing

Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public class CaptureWin32 {
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hWnd, out RECT rect);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr hWnd, IntPtr hdc, uint flags);
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr hWnd, int cmd);
  [DllImport("shcore.dll")] public static extern int SetProcessDpiAwareness(int value);
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left; public int Top; public int Right; public int Bottom; }
}
"@

# Physical pixels, otherwise the captured bitmap is the 125%-scaled logical size and crops content.
[CaptureWin32]::SetProcessDpiAwareness(2) | Out-Null

$proc = Start-Process conhost.exe -ArgumentList ('powershell.exe -NoProfile -ExecutionPolicy Bypass -File "' + $Wrapper + '"') -PassThru

$done = $Title
$deadline = (Get-Date).AddSeconds(180)
$hwnd = [IntPtr]::Zero
while ($hwnd -eq [IntPtr]::Zero -and (Get-Date) -lt $deadline) {
  Start-Sleep -Milliseconds 400
  $win = Get-Process | Where-Object { $_.MainWindowTitle -eq $done } | Select-Object -First 1
  if ($win) { $hwnd = $win.MainWindowHandle }
}
if ($hwnd -eq [IntPtr]::Zero) {
  Write-Output "WINDOW-NOT-READY: $done"
  taskkill /PID $proc.Id /T /F 2>&1 | Out-Null
  exit 1
}

[CaptureWin32]::ShowWindow($hwnd, 9) | Out-Null   # SW_RESTORE (window starts minimised)
Start-Sleep -Milliseconds 1200

$rect = New-Object CaptureWin32+RECT
for ($i = 0; $i -lt 10; $i++) {
  [CaptureWin32]::GetWindowRect($hwnd, [ref]$rect) | Out-Null
  if (($rect.Right - $rect.Left) -gt 400) { break }
  Start-Sleep -Milliseconds 500
}
$w = $rect.Right - $rect.Left
$h = $rect.Bottom - $rect.Top
Write-Output "window=${w}x${h}"

$bmp = New-Object System.Drawing.Bitmap $w, $h
$g = [System.Drawing.Graphics]::FromImage($bmp)
$hdc = $g.GetHdc()
$ok = [CaptureWin32]::PrintWindow($hwnd, $hdc, 2)   # PW_RENDERFULLCONTENT
$g.ReleaseHdc($hdc)
$g.Dispose()

# Detect an all-one-colour result (PrintWindow silently failing on some hosts).
$colors = New-Object System.Collections.Generic.HashSet[int]
for ($y = 0; $y -lt $h; $y += 7) {
  for ($x = 0; $x -lt $w; $x += 7) { [void]$colors.Add($bmp.GetPixel($x, $y).ToArgb()) }
}
Write-Output "printwindow=$ok distinctColors=$($colors.Count)"

# Trim the empty rows below the last line of output (padding stays for readability).
$lastRow = 0
for ($y = $h - 8; $y -ge 0; $y--) {
  $rowHasInk = $false
  for ($x = 2; $x -lt ($w - 40); $x += 3) {
    $c = $bmp.GetPixel($x, $y)
    if ($c.R -gt 24 -or $c.G -gt 24 -or $c.B -gt 24) { $rowHasInk = $true; break }
  }
  if ($rowHasInk) { $lastRow = $y; break }
}
$cropH = [Math]::Min($h, $lastRow + 18)
if ($cropH -gt 40 -and $cropH -lt $h) {
  $cropped = $bmp.Clone((New-Object System.Drawing.Rectangle 0, 0, $w, $cropH), $bmp.PixelFormat)
  $bmp.Dispose()
  $bmp = $cropped
  Write-Output "cropped to ${w}x${cropH}"
}

$bmp.Save($Out, [System.Drawing.Imaging.ImageFormat]::Png)
$bmp.Dispose()

taskkill /PID $proc.Id /T /F 2>&1 | Out-Null
Write-Output "saved $Out"
