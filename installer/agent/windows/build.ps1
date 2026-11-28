<#
  Builds crspagent-windows-<amd64|arm64>.msi (CRSP348) with WiX v5. No user interface.

    pwsh installer/agent/windows/build.ps1 -Arch x64 -Exe out/crsp.exe -Version 0.1.0 -Out out

  -Version is "1.2.3" or "v1.2.3-rc1" (stamped 1.2.3.0), or "edge-<sha>" (stamped <EdgeBase>.<Build>),
  the same mapping as installer/controller/build.ps1.
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory)][ValidateSet('x64', 'arm64')][string]$Arch,
  [Parameter(Mandatory)][string]$Exe,
  [Parameter(Mandatory)][string]$Version,
  [string]$Out = (Join-Path $PSScriptRoot 'out'),
  [string]$EdgeBase = '0.1.0',
  [int]$Build = 0
)
$ErrorActionPreference = 'Stop'
$here = $PSScriptRoot
$controller = Join-Path (Split-Path (Split-Path $here -Parent) -Parent) 'controller'
if ($Version -match '^v?(\d+)\.(\d+)\.(\d+)') {
  $ProductVersion = "$($Matches[1]).$($Matches[2]).$($Matches[3]).0"
} elseif ($Version -like 'edge*') {
  $ProductVersion = "$EdgeBase.$Build"
} else {
  throw "Cannot map version '$Version' to an MSI ProductVersion."
}
$Exe = (Resolve-Path $Exe).Path
New-Item -ItemType Directory -Force -Path $Out | Out-Null
$Out = (Resolve-Path $Out).Path
$goarch = if ($Arch -eq 'x64') { 'amd64' } else { 'arm64' }
$msi = Join-Path $Out "crspagent-windows-$goarch.msi"
# The WiX tool and its extension versions are the Controller installer's (dotnet-tools.json).
Push-Location $controller
try {
  dotnet tool restore | Out-Host
  if ($LASTEXITCODE -ne 0) { throw 'dotnet tool restore failed' }
  dotnet wix extension add 'WixToolset.Util.wixext/5.0.2' | Out-Host
  if ($LASTEXITCODE -ne 0) { throw 'wix extension add failed' }
  dotnet wix build (Join-Path $here 'crspagent.wxs') -arch $Arch -ext WixToolset.Util.wixext `
    -d "ProductVersion=$ProductVersion" -d "CrspExe=$Exe" -o $msi | Out-Host
  if ($LASTEXITCODE -ne 0) { throw 'wix build failed' }
} finally { Pop-Location }
Write-Host "Built $msi (ProductVersion $ProductVersion)"
