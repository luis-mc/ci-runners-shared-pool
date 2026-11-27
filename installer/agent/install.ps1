# CRSP304: the agent installer for Windows. Run in PowerShell as Administrator.
# It copies crsp.exe to %ProgramFiles%\crsp (where the controller installer puts it too) and stops.
# Joining the controller is the next command (crsp.exe agent install --controller ... --join-code ...),
# which the controller shows or runs over SSH.
$ErrorActionPreference = 'Stop'
$id = [Security.Principal.WindowsIdentity]::GetCurrent()
if (-not (New-Object Security.Principal.WindowsPrincipal $id).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  throw 'run this in PowerShell as Administrator'
}
$dest = Join-Path $env:ProgramFiles 'crsp'
New-Item -ItemType Directory -Force -Path $dest | Out-Null
Copy-Item -Force (Join-Path $PSScriptRoot 'crsp.exe') (Join-Path $dest 'crsp.exe')
Write-Output ('crsp.exe is in ' + $dest)
