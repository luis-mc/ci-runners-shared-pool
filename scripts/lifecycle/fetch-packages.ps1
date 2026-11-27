# OPS69: builds the packages one Windows lifecycle lane needs and sets LC_PKG, LC_PREV_PKG, LC_BROKEN_PKG, LC_OLDER_PKG.
#   pwsh scripts/lifecycle/fetch-packages.ps1 -Arch x64|arm64
# The MSIs come from this branch (never from a release), except the previous one, which is the newest
# published release. Names are Crsp-<version>-win-<arch>.msi, the release names (the older release names
# are tried second, for the one transition release).
[CmdletBinding()]
param([Parameter(Mandatory)][ValidateSet('x64', 'arm64')][string]$Arch)
$ErrorActionPreference = 'Stop'
$repo = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$out = Join-Path $repo 'installer/out/lifecycle'
New-Item -ItemType Directory -Force -Path $out | Out-Null
$goarch = if ($Arch -eq 'x64') { 'amd64' } else { 'arm64' }

$env:GOOS = 'windows'; $env:GOARCH = $goarch; $env:CGO_ENABLED = '0'
go build -trimpath -ldflags '-s -w -X main.version=0.9.0' -o (Join-Path $out 'crsp.exe') ./apps/controller/cmd/crsp
if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED

function Build-Msi([string]$version, [switch]$Fail) {
  $dir = Join-Path $out "v$version"
  $a = @{ Arch = $Arch; Exe = (Join-Path $out 'crsp.exe'); Version = $version; Out = $dir }
  if ($Fail) { $a.FailAfterInstall = $true }
  & (Join-Path $repo 'installer/build.ps1') @a | Out-Host
  $built = Join-Path $dir "crsp-windows-$goarch.msi"
  $named = Join-Path $dir "Crsp-$version-win-$Arch.msi"
  Copy-Item $built $named -Force
  return (Resolve-Path $named).Path
}
$pkg = Build-Msi '0.9.0'          # this build
$broken = Build-Msi '0.9.1' -Fail # a newer build that fails after its files are in: must roll back
$older = Build-Msi '0.0.9'        # older than this build: must be refused

# The previous release: the newest published release MSI for this architecture, new name first, old name second.
$prevDir = Join-Path $out 'previous'
New-Item -ItemType Directory -Force -Path $prevDir | Out-Null
$prev = $null
$tags = @(gh release list --repo $env:GITHUB_REPOSITORY --exclude-pre-releases --limit 10 --json tagName --jq '.[].tagName')
foreach ($tag in $tags) {
  foreach ($pattern in "Crsp-*-win-$Arch.msi", "crsp-windows-$goarch*.msi") {
    gh release download $tag --repo $env:GITHUB_REPOSITORY --pattern $pattern --dir $prevDir --clobber 2>$null
    $f = Get-ChildItem $prevDir -Filter '*.msi' -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($LASTEXITCODE -eq 0 -and $f) { $prev = $f.FullName; Write-Host "previous release: $tag ($($f.Name))"; break }
  }
  if ($prev) { break }
}
if (-not $prev) { throw 'no previous release MSI could be downloaded' }

foreach ($kv in @{ LC_PKG = $pkg; LC_PREV_PKG = $prev; LC_BROKEN_PKG = $broken; LC_OLDER_PKG = $older }.GetEnumerator()) {
  "$($kv.Key)=$($kv.Value)" | Out-File -FilePath $env:GITHUB_ENV -Append -Encoding utf8
  Write-Host "$($kv.Key)=$($kv.Value)"
}
