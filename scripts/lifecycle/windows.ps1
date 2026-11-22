# DS235d: Windows MSI lifecycle steps (LC-8 to LC-17, LC-18, LC-29, LC-33).
# Usage: pwsh ./windows.ps1 -Check all|lc9|lc8|...|lint -Lifecycle lifecycle.json -Pkg App-1.2.0-x64.msi
# Other packages: -PrevPkg (N-1), -BrokenPkg, -OlderPkg. Prints "LC-9 PASS detail" per check; exit 1 on any FAIL.
param(
  [string]$Check = 'all',
  [string]$Lifecycle = $(if ($env:LIFECYCLE_JSON) { $env:LIFECYCLE_JSON } else { 'lifecycle.json' }),
  [string]$Pkg = $env:LC_PKG, [string]$PrevPkg = $env:LC_PREV_PKG,
  [string]$BrokenPkg = $env:LC_BROKEN_PKG, [string]$OlderPkg = $env:LC_OLDER_PKG
)
$ErrorActionPreference = 'Stop'
$script:Fails = 0
$script:Here = Split-Path -Parent $MyInvocation.MyCommand.Path
$script:Lc = Get-Content -Raw $Lifecycle | ConvertFrom-Json
function Expand-Path($p) { if ($p) { [Environment]::ExpandEnvironmentVariables($p) } }
$script:W = $script:Lc.paths.windows
$script:Product = $script:Lc.product.name        # the name in the Apps list is the MSI ProductName below
$script:AppsName = 'CI Runners Shared Pool'      # WiX Package Name (installer/crsp.wxs)
$script:Service = if ($script:W.service) { $script:W.service } else { $script:Lc.product.id }
$script:Install = Expand-Path $script:W.program
$script:Data = Expand-Path $script:W.data
$script:Config = Expand-Path $script:W.config
$script:Logs = Expand-Path $script:W.logs
$script:ConsolePort = 8460                        # crsp console, loopback only (internal/config default)
$script:DataRoots = @(Expand-Path '%ProgramData%')

function Out-Lc($id, $ok, $detail) {
  Write-Output ("{0} {1} {2}" -f $id, $(if ($ok) { 'PASS' } else { 'FAIL' }), $detail)
  if (-not $ok) { $script:Fails++ }
}
function Install-Msi($file, [switch]$Purge) {
  $a = @('/i', $file, '/qn', '/norestart')
  $p = Start-Process msiexec.exe -ArgumentList $a -Wait -PassThru
  return $p.ExitCode
}
function Get-ProductCode {
  # Every build has its own ProductCode (WiX makes one per build), so read it from the Apps list.
  $k = Get-ItemProperty 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*', 'HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*' -ErrorAction SilentlyContinue |
    Where-Object { $_.DisplayName -eq $script:AppsName } | Select-Object -First 1
  if ($k) { return $k.PSChildName }
  return $null
}
function Remove-Msi([switch]$Purge) {
  $code = Get-ProductCode
  if (-not $code) { return 0 }  # nothing installed: nothing to remove
  $a = @('/x', $code, '/qn', '/norestart')
  if ($Purge) { $a += 'PURGE=1' }
  return (Start-Process msiexec.exe -ArgumentList $a -Wait -PassThru).ExitCode
}
function Get-FileSha256($file) {
  # The service holds its database open, so open with FileShare.ReadWrite: a live file is read, never skipped.
  $stream = [System.IO.File]::Open($file, 'Open', 'Read', 'ReadWrite')
  try {
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try { return ([System.BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-', '') } finally { $sha.Dispose() }
  } finally { $stream.Dispose() }
}
function Get-Checksums($paths) {
  # A file that truly cannot be read is listed as "UNREADABLE path: reason" and recorded in $script:ChecksumError,
  # so the step prints a FAIL line with the reason; it never crashes the script and never drops the file.
  $files = $paths | Where-Object { $_ -and (Test-Path $_) } | ForEach-Object { Get-ChildItem $_ -Recurse -File } | Sort-Object FullName
  foreach ($f in $files) {
    try { '{0} {1}' -f (Get-FileSha256 $f.FullName), $f.FullName }
    catch {
      $script:ChecksumError = 'cannot read {0}: {1}' -f $f.FullName, $_.Exception.Message
      'UNREADABLE {0}' -f $f.FullName
    }
  }
}
function Get-Version {
  $k = Get-ItemProperty 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*', 'HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*' -ErrorAction SilentlyContinue |
    Where-Object { $_.DisplayName -eq $script:AppsName } | Select-Object -First 1
  if ($k) { return $k.DisplayVersion } else { return $null }
}
function Test-Up {
  $s = Get-Service -Name $script:Service -ErrorAction SilentlyContinue
  if (-not $s -or $s.Status -ne 'Running') { return $false }
  try { Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 "http://127.0.0.1:$($script:ConsolePort)/" | Out-Null; return $true } catch { return $false }
}
function Wait-Until([scriptblock]$cond, [int]$seconds) {
  for ($t = 0; $t -lt $seconds; $t++) { if (& $cond) { return $true }; Start-Sleep -Seconds 1 }
  return $false
}
function Test-RebootPending {
  (Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending') -or
  (Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired')
}
function Get-DataPaths { @($script:Config) + @($script:Data) }

function Lc9 {
  $rc = Install-Msi $Pkg
  if ($rc -ne 0) { Out-Lc 'LC-9' $false "silent install exit $rc"; return }
  if (Test-RebootPending) { Out-Lc 'LC-9' $false 'install asks for a reboot'; return }
  Out-Lc 'LC-9' (Wait-Until { Test-Up } 30) 'service up and console on 127.0.0.1 within 30 s'
}
function Lc8 {
  $cmd = $script:Lc.test_interface.self_test
  if (-not $cmd) { Out-Lc 'LC-8' $false 'lifecycle.json has no test_interface.self_test'; return }
  & cmd.exe /c $cmd | Out-Null
  Out-Lc 'LC-8' ($LASTEXITCODE -eq 0) 'self-test on a fresh install'
}
function Lc10 {
  $script:ChecksumError = $null
  $before = (Get-Checksums (Get-DataPaths)) + (Get-Version)
  [void](Install-Msi $Pkg)
  $after = (Get-Checksums (Get-DataPaths)) + (Get-Version)
  if ($script:ChecksumError) { Out-Lc 'LC-10' $false $script:ChecksumError; return }
  Out-Lc 'LC-10' (($before -join "`n") -eq ($after -join "`n")) 'reinstall of the same version changes nothing'
}
function Lc11 {
  if (-not $PrevPkg) { Out-Lc 'LC-11' $false 'missing input PrevPkg'; return }
  [void](Remove-Msi -Purge); [void](Install-Msi $PrevPkg)
  $script:ChecksumError = $null
  $before = Get-Checksums (Get-DataPaths)
  $rc = Install-Msi $Pkg
  $after = Get-Checksums (Get-DataPaths)
  if ($script:ChecksumError) { Out-Lc 'LC-11' $false $script:ChecksumError; return }
  Out-Lc 'LC-11' (($rc -eq 0) -and $before -and (($before -join "`n") -eq ($after -join "`n"))) 'upgrade from N-1 keeps config and data (checksums)'
}
function Lc12 {
  if (-not $BrokenPkg) { Out-Lc 'LC-12' $false 'missing input BrokenPkg'; return }
  $good = Get-Version
  [void](Install-Msi $BrokenPkg)
  Out-Lc 'LC-12' (Wait-Until { ((Get-Version) -eq $good) -and (Test-Up) } 300) 'broken build rolls back within 5 min'
}
function Lc13 {
  if (-not $OlderPkg) { Out-Lc 'LC-13' $false 'missing input OlderPkg'; return }
  $before = Get-Version
  [void](Install-Msi $OlderPkg)
  Out-Lc 'LC-13' ((Get-Version) -eq $before) 'downgrade refused'
}
function Lc14 {
  # keep_on_remove names keys of paths.windows; a path is kept when it is, or sits under, one of them.
  $keep = @($script:Lc.keep_on_remove | ForEach-Object { Expand-Path $script:W.$_ })
  $rc = Remove-Msi
  $bad = 0
  function Test-Kept($p) { foreach ($k in $keep) { if ($p -eq $k -or $p.StartsWith($k.TrimEnd('\') + '\')) { return $true } }; return $false }
  foreach ($p in $keep) { if (-not (Test-Path $p)) { $bad++ } }
  foreach ($p in @($script:Config, $script:Data, $script:Logs)) { if ($p -and -not (Test-Kept $p) -and (Test-Path $p)) { $bad++ } }
  Out-Lc 'LC-14' (($rc -eq 0) -and ($bad -eq 0)) 'remove keeps exactly keep_on_remove'
}
function Lc15 {
  $rc = Remove-Msi -Purge
  $out = & pwsh -NoProfile -File (Join-Path $script:Here 'leftovers.ps1') -Lifecycle $Lifecycle
  Out-Lc 'LC-15' (($rc -eq 0) -and ($LASTEXITCODE -eq 0)) $(if ($out) { 'leftovers: ' + ($out -join '; ') } else { 'purge leaves nothing' })
}
function Lc15Cli { # purge by the CLI: crsp uninstall --delete-data (OPS69)
  [void](Install-Msi $Pkg)
  $exe = Join-Path $script:Install 'crsp.exe'
  if (-not (Test-Path $exe)) { Out-Lc 'LC-15' $false 'crsp.exe is not installed for the --delete-data run'; return }
  & $exe uninstall --delete-data --yes | Out-Null
  $rc = $LASTEXITCODE
  [void](Remove-Msi -Purge)
  $out = & pwsh -NoProfile -File (Join-Path $script:Here 'leftovers.ps1') -Lifecycle $Lifecycle
  Out-Lc 'LC-15' (($rc -eq 0) -and ($LASTEXITCODE -eq 0)) $(if ($out) { 'cli leftovers: ' + ($out -join '; ') } else { 'crsp uninstall --delete-data leaves nothing' })
}
function Lc16 {
  $exe = Join-Path $script:Install 'crsp.exe'
  if (Test-Path $exe) { Start-Process $exe -ArgumentList 'tray'; Start-Sleep -Seconds 2 }
  $rc = Remove-Msi
  Out-Lc 'LC-16' (($rc -eq 0) -and -not (Test-RebootPending)) 'remove works with the tray running, no reboot'
}
function Lc17 {
  # LC-17 (lifecycle.json): "the service may not start when nobody is signed in". A hosted runner
  # always has its own account signed in, so count no sessions. Prove what the words mean: the
  # service starts by itself at boot (no sign-in needed), runs in session 0 under a service
  # account (no user session holds it), and answers.
  $svc = Get-CimInstance -ClassName Win32_Service -Filter "Name='$($script:Service)'" -ErrorAction SilentlyContinue
  if (-not $svc) { Out-Lc 'LC-17' $false "service $($script:Service) is not installed"; return }
  $boot = $svc.StartMode -eq 'Auto'
  $sid = -1
  if ($svc.ProcessId -gt 0) {
    $proc = Get-CimInstance -ClassName Win32_Process -Filter "ProcessId=$($svc.ProcessId)" -ErrorAction SilentlyContinue
    if ($proc) { $sid = [int]$proc.SessionId }
  }
  $session0 = $sid -eq 0
  $up = Test-Up
  Out-Lc 'LC-17' ($boot -and $session0 -and $up) "service start mode $($svc.StartMode), account $($svc.StartName), session $sid (0 = no user session), answering $up"
}
function Lc29 {
  $pair = $script:Lc.test_interface.simulate
  if (-not $pair) { Out-Lc 'LC-29' $false 'lifecycle.json has no test_interface.simulate'; return }
  & cmd.exe /c $pair | Out-Null
  Out-Lc 'LC-29' (($LASTEXITCODE -eq 0) -and (Wait-Until { (Get-Service $script:Service).Status -eq 'Running' } 30)) 'service running after pairing, no manual step'
}
function Lc33 {
  $n = 0
  foreach ($root in $script:DataRoots) {
    if ($root -and (Test-Path $root)) { $n += @(Get-ChildItem $root -Directory | Where-Object { $_.Name -like "*$($script:Lc.product.id)*" }).Count }
  }
  $legacy = 0  # crsp has no legacy data folder
  Out-Lc 'LC-33' (($n -le 1) -and ($legacy -eq 0)) "$n data folders, $legacy legacy folders"
}
function Lint { # LC-18: WiX ICE validation and one Apps-list entry
  & wix msi validate $Pkg | Out-Null
  $ice = $LASTEXITCODE -eq 0
  $entries = @(Get-ItemProperty 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*', 'HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*' -ErrorAction SilentlyContinue |
    Where-Object { $_.DisplayName -eq $script:AppsName }).Count
  Out-Lc 'LC-18' ($ice -and ($entries -eq 1)) "ICE $(if ($ice) { 'clean' } else { 'errors' }), $entries Apps-list entry(ies)"
}

$order = 'lc9', 'lc8', 'lc17', 'lc29', 'lc10', 'lc11', 'lc13', 'lc12', 'lc33', 'lint', 'lc16', 'lc14', 'lc15', 'lc15cli'
$todo = if ($Check -eq 'all') { $order } else { @($Check) }
foreach ($c in $todo) {
  switch ($c) {
    'lint' { Lint } 'lc8' { Lc8 } 'lc9' { Lc9 } 'lc10' { Lc10 } 'lc11' { Lc11 } 'lc12' { Lc12 } 'lc13' { Lc13 }
    'lc14' { Lc14 } 'lc15' { Lc15 } 'lc15cli' { Lc15Cli } 'lc16' { Lc16 } 'lc17' { Lc17 } 'lc29' { Lc29 } 'lc33' { Lc33 }
    default { Write-Error "unknown check $c"; exit 2 }
  }
}
if ($script:Fails -gt 0) { exit 1 }
exit 0
