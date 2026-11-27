<#
  Builds crsp-windows-<amd64|arm64>.msi (operations R5.7) with WiX v5.

    pwsh installer/controller/build.ps1 -Arch x64 -Exe out/crsp.exe -Version 0.1.0 -Out installer/controller/out

  -Version is the release version the build workflow knows: "1.2.3" or "v1.2.3-rc1"
  (stamped as 1.2.3.0), or "edge-<sha>" (stamped <EdgeBase>.<Build>, the next
  patch after the latest stable release; Windows Installer compares the first
  three fields only, so a later edge build replaces an earlier one). -ProductVersion
  overrides the mapping.
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory)][ValidateSet('x64', 'arm64')][string]$Arch,
  [Parameter(Mandatory)][string]$Exe,
  [Parameter(Mandatory)][string]$Version,
  [string]$Out = (Join-Path $PSScriptRoot 'out'),
  [string]$EdgeBase = '0.1.0',
  [int]$Build = 0,
  [string]$ProductVersion = '',
  [switch]$FailAfterInstall
)
$ErrorActionPreference = 'Stop'
$here = $PSScriptRoot
$repo = Split-Path (Split-Path $here -Parent) -Parent

if (-not $ProductVersion) {
  if ($Version -match '^v?(\d+)\.(\d+)\.(\d+)') {
    $ProductVersion = "$($Matches[1]).$($Matches[2]).$($Matches[3]).0"
  } elseif ($Version -like 'edge*') {
    $ProductVersion = "$EdgeBase.$Build"
  } else {
    throw "Cannot map version '$Version' to an MSI ProductVersion; pass -ProductVersion."
  }
}
if ($ProductVersion -notmatch '^\d{1,5}\.\d{1,5}\.\d{1,5}(\.\d{1,5})?$') { throw "Bad ProductVersion '$ProductVersion'." }

$Exe = (Resolve-Path $Exe).Path
New-Item -ItemType Directory -Force -Path $Out | Out-Null
$Out = (Resolve-Path $Out).Path
$work = Join-Path $Out "work-$Arch"
New-Item -ItemType Directory -Force -Path $work | Out-Null

# The licence page: the MIT text from LICENSE as RTF.
$lic = (Get-Content -Raw (Join-Path $repo 'LICENSE')) -replace '\\', '\\' -replace '\{', '\{' -replace '\}', '\}'
$lic = ($lic -replace "`r?`n", "\par`n")
$rtf = Join-Path $work 'license.rtf'
Set-Content -Path $rtf -Encoding ASCII -Value ("{\rtf1\ansi\deff0{\fonttbl{\f0 Segoe UI;}}\f0\fs20 " + $lic + "}")

# The installer art (TRAY5 D12, D17): banner and dialog bitmaps drawn from the design tokens.
$art = Join-Path $work 'art'
go run ./apps/controller/internal/updatewindow/genart -tokens (Join-Path $repo 'packages/ui/src/tokens.css') -bmp $art
if ($LASTEXITCODE -ne 0) { throw 'installer art failed' }

# The file name uses the Go architecture name, like the release zips: crsp-windows-amd64.msi.
$goarch = if ($Arch -eq 'x64') { 'amd64' } else { 'arm64' }
$msi = Join-Path $Out "crsp-windows-$goarch.msi"
Push-Location $here
try {
  dotnet tool restore | Out-Host
  if ($LASTEXITCODE -ne 0) { throw 'dotnet tool restore failed' }
  foreach ($ext in 'WixToolset.UI.wixext', 'WixToolset.Util.wixext', 'WixToolset.Firewall.wixext') {
    dotnet wix extension add "$ext/5.0.2" | Out-Host
    if ($LASTEXITCODE -ne 0) { throw "wix extension add $ext failed" }
  }
  dotnet wix build crsp.wxs -arch $Arch `
    -ext WixToolset.UI.wixext -ext WixToolset.Util.wixext -ext WixToolset.Firewall.wixext `
    -d "ProductVersion=$ProductVersion" -d "CrspExe=$Exe" `
    -d "LicenseRtf=$rtf" `
    -d "BannerBmp=$(Join-Path $art 'banner.bmp')" -d "DialogBmp=$(Join-Path $art 'dialog.bmp')" `
    -d "CrspIcon=$(Join-Path $repo 'apps/controller/cmd/crsp/winres/crsp.ico')" `
    @(if ($FailAfterInstall) { '-d'; 'FailAfterInstall=1' }) `
    -o $msi | Out-Host
  if ($LASTEXITCODE -ne 0) { throw 'wix build failed' }
} finally { Pop-Location }
Remove-Item -Recurse -Force $work
Write-Host "Built $msi (ProductVersion $ProductVersion)"
