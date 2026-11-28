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
$script:AppsName = 'CI Runners Shared Pool'      # WiX Package Name (installer/controller/crsp.wxs)
$script:Service = if ($script:W.service) { $script:W.service } else { $script:Lc.product.id }
$script:Install = Expand-Path $script:W.program
$script:Data = Expand-Path $script:W.data
$script:Config = Expand-Path $script:W.config
$script:Logs = Expand-Path $script:W.logs
$script:ConsolePort = 8460                        # crsp console, loopback only (apps/controller/internal/config default)
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
function Get-LogDir {
  # One folder for every log the workflow uploads (runner.temp/lifecycle-logs), so the artifact has one root.
  $d = Join-Path $(if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { $env:TEMP }) 'lifecycle-logs'
  if (-not (Test-Path $d)) { [void](New-Item -ItemType Directory -Path $d -Force) }
  return $d
}
function Remove-Msi([switch]$Purge) {
  $code = Get-ProductCode
  if (-not $code) { return 0 }  # nothing installed: nothing to remove
  $script:MsiRemoveCount++
  $log = Join-Path (Get-LogDir) ("msi-remove-{0}.log" -f $script:MsiRemoveCount)
  $script:LastMsiRemoveLog = $log
  $a = @('/x', $code, '/qn', '/norestart', '/l*v', $log)
  if ($Purge) { $a += 'PURGE=1' }
  return (Start-Process msiexec.exe -ArgumentList $a -Wait -PassThru).ExitCode
}
$script:MsiRemoveCount = 0
$script:LastMsiRemoveLog = $null
function Show-MsiLog($log) {
  # LC-15 evidence: what the verbose log says about the purge actions (property value, whether it ran, return value).
  if (-not $log -or -not (Test-Path $log)) { Write-Output "msi log: none ($log)"; return }
  $lines = @(Get-Content -LiteralPath $log -ErrorAction SilentlyContinue)
  $hits = for ($i = 0; $i -lt $lines.Count; $i++) { if ($lines[$i] -match 'PurgeFolder|SetPurgeFolder|PURGE|PurgeFolder:') { $i } }
  $show = [System.Collections.Generic.SortedSet[int]]::new()
  foreach ($h in $hits) { for ($j = [Math]::Max(0, $h - 1); $j -le [Math]::Min($lines.Count - 1, $h + 4); $j++) { [void]$show.Add($j) } }
  Write-Output "msi log $log ($($lines.Count) lines), lines around PurgeFolder/SetPurgeFolder/PURGE:"
  foreach ($j in ($show | Select-Object -First 120)) { Write-Output ("  {0}: {1}" -f ($j + 1), $lines[$j]) }
}
function Show-MsiAllUsers($file) {
  # LC-18 evidence: the built MSI's ALLUSERS property (ICE43/ICE57 treat the Start menu as per-user unless it is 1).
  try {
    $inst = New-Object -ComObject WindowsInstaller.Installer
    $db = $inst.GetType().InvokeMember('OpenDatabase', 'InvokeMethod', $null, $inst, @((Resolve-Path $file).Path, 0))
    $v = $db.GetType().InvokeMember('OpenView', 'InvokeMethod', $null, $db, @("SELECT `Property`,`Value` FROM `Property` WHERE `Property`='ALLUSERS' OR `Property`='MSIINSTALLPERUSER'"))
    $v.GetType().InvokeMember('Execute', 'InvokeMethod', $null, $v, $null)
    $found = $false
    while ($r = $v.GetType().InvokeMember('Fetch', 'InvokeMethod', $null, $v, $null)) {
      $found = $true
      Write-Output ('msi property {0}={1}' -f $r.GetType().InvokeMember('StringData', 'GetProperty', $null, $r, 1), $r.GetType().InvokeMember('StringData', 'GetProperty', $null, $r, 2))
    }
    if (-not $found) { Write-Output 'msi property ALLUSERS: not in the Property table' }
  } catch { Write-Output "msi property read failed: $_" }
}
function Get-FileSha256($file) {
  # A file that is read while the service runs is never skipped: open with FileShare.ReadWrite.
  $stream = [System.IO.File]::Open($file, 'Open', 'Read', 'ReadWrite')
  try {
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try { return ([System.BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-', '') } finally { $sha.Dispose() }
  } finally { $stream.Dispose() }
}
# What the checksums leave out, each by name. The contract names three places the user keeps: paths.windows.config
# (the config file), paths.windows.data (the data folder, kept on remove: keep_on_remove) and, apart from both,
# paths.windows.logs. So:
#  - the logs folder is not config and not data: the service writes it each time it starts;
#  - controller.db is the data, compared by what it holds (its user tables), not by the bytes of a file the service
#    rewrites; controller.db-wal and controller.db-shm are SQLite's journal of that same database, which a checkpoint
#    folds into controller.db, so they carry no data of their own.
# Everything else under config and data is compared byte for byte.
$script:DbName = 'controller.db'
# logs.db is the log store (observability R6.10): its own file, opened and migrated by the service on every start and
# regenerable, so its bytes change without any user data changing. Like the logs folder it is left out by name,
# with its journal files.
$script:LogDbName = 'logs.db'
$script:VolatileNames = @($script:DbName, "$($script:DbName)-wal", "$($script:DbName)-shm",
  $script:LogDbName, "$($script:LogDbName)-wal", "$($script:LogDbName)-shm")
# Runtime state of the controller's own agent that the service rewrites on every start, by name. lifecycle.json
# names as config and data only paths.config and paths.data (keep_on_remove: ["data"]) and has no runtime or cache
# location, so these two files stay where they are and are left out of the comparison by name:
#   agent\local-join  a one-time credential, new each start and every 5 min, valid 10 min
#                     (apps/controller/cmd/crsp/machines_wiring.go writeLocalCredential; apps/controller/internal/machines/service.go LocalCredential).
#   agent\hostuse.json  the measured host use, restored at start and saved every 5 min and at stop
#                     (apps/controller/internal/agent/run.go sampleLoop: usePath = DataDir/hostuse.json).
$script:RuntimeFiles = @('agent\local-join', 'agent\hostuse.json')
# Download caches under the data folder, by folder. agent-installers\<version>\ holds the agent packages and
# SHA256SUMS the controller fetches from the release and checks (apps/controller/internal/agentinstallers, CRSP304):
# regenerable, and filled in the background after each start, so an upgrade adds files without any user data changing.
$script:CacheDirs = @('agent-installers')
# Files the new build creates on its first start when the previous release did not have them. An upgrade may ADD
# them (the secret is made once, caches R5.80); it may never change or remove one that was there.
#   mirrors\packages.key  the package proxy secret (apps/controller/internal/lancache/pkgproxy.go LoadSecret keeps an existing one)
$script:MayBeAdded = @('mirrors\packages.key')
function Test-VolatilePath($full) {
  $name = Split-Path -Leaf $full
  if ($script:VolatileNames -contains $name) { return $true }
  if ($script:Data) {
    $root = $script:Data.TrimEnd('\') + '\'
    foreach ($r in $script:RuntimeFiles) {
      if ($full.Equals($root + $r, [System.StringComparison]::OrdinalIgnoreCase)) { return $true }
    }
    foreach ($c in $script:CacheDirs) {
      if ($full.StartsWith($root + $c + '\', [System.StringComparison]::OrdinalIgnoreCase)) { return $true }
    }
  }
  if ($script:Logs) {
    $logs = $script:Logs.TrimEnd('\') + '\'
    if ($full.StartsWith($logs, [System.StringComparison]::OrdinalIgnoreCase)) { return $true }
  }
  return $false
}
function Get-Checksums($paths) {
  # A file that truly cannot be read is listed as "UNREADABLE path" and recorded in $script:ChecksumError,
  # so the step prints a FAIL line with the reason; it never crashes the script and never drops the file.
  $files = $paths | Where-Object { $_ -and (Test-Path $_) } | ForEach-Object { Get-ChildItem $_ -Recurse -File } |
    Where-Object { -not (Test-VolatilePath $_.FullName) } | Sort-Object FullName
  foreach ($f in $files) {
    try { '{0} {1}' -f (Get-FileSha256 $f.FullName), $f.FullName }
    catch {
      $script:ChecksumError = 'cannot read {0}: {1}' -f $f.FullName, $_.Exception.Message
      'UNREADABLE {0}' -f $f.FullName
    }
  }
}
function Invoke-LifecycleTool($arguments) {
  # Start-Process, so a message on stderr is text to read and never an error that stops this script (Windows PowerShell 5.1).
  $tool = Get-Command crsp-lifecycle-test -ErrorAction SilentlyContinue
  if (-not $tool) { return [pscustomobject]@{ Code = 1; Text = 'crsp-lifecycle-test is not on the PATH' } }
  $err = Join-Path ([System.IO.Path]::GetTempPath()) ('lct-' + [guid]::NewGuid().ToString('N') + '.txt')
  try {
    $out = Join-Path ([System.IO.Path]::GetTempPath()) ('lct-' + [guid]::NewGuid().ToString('N') + '-out.txt')
    $p = Start-Process -FilePath $tool.Source -ArgumentList $arguments -Wait -PassThru -NoNewWindow -RedirectStandardError $err -RedirectStandardOutput $out
    $text = if (Test-Path $err) { (Get-Content -Raw $err) } else { '' }
    $stdout = if (Test-Path $out) { (Get-Content -Raw $out) } else { '' }
    return [pscustomobject]@{ Code = $p.ExitCode; Text = ("$text").Trim(); Out = ("$stdout").Trim() }
  } finally { Remove-Item $err, $out -Force -ErrorAction SilentlyContinue }
}
function Stop-Crsp {
  # Stop the service and wait until it has stopped, so the files are quiet. False when it did not stop.
  $s = Get-Service -Name $script:Service -ErrorAction SilentlyContinue
  if (-not $s) { return $true }
  [void](Wait-Until { (Get-Service -Name $script:Service).Status -notin 'StartPending', 'StopPending' } 60)
  try { Stop-Service -Name $script:Service -Force -ErrorAction Stop } catch { }
  return (Wait-Until { (Get-Service -Name $script:Service).Status -eq 'Stopped' } 90)
}
function Start-Crsp {
  $s = Get-Service -Name $script:Service -ErrorAction SilentlyContinue
  if (-not $s) { return $false }
  try { Start-Service -Name $script:Service -ErrorAction Stop } catch { }
  return (Wait-Until { Test-Up } 60)
}
function Get-Snapshot($label) {
  # Stops the service first (files quiet), then records the config and data: the checksums of every file that is not
  # volatile, and the user tables of the database.
  $script:ChecksumError = $null
  if (-not (Stop-Crsp)) { return [pscustomobject]@{ Error = "the $($script:Service) service did not stop before the $label snapshot" } }
  $db = Join-Path $script:Data $script:DbName
  $json = $null
  if (Test-Path $db) {
    $json = Join-Path ([System.IO.Path]::GetTempPath()) ("lc-db-$label-" + [guid]::NewGuid().ToString('N') + '.json')
    $r = Invoke-LifecycleTool @('--db-snapshot', "`"$db`"", "`"$json`"")
    if ($r.Code -ne 0) { return [pscustomobject]@{ Error = "cannot read the database for the $label snapshot: $($r.Text)" } }
  }
  if ($script:ChecksumError) { return [pscustomobject]@{ Error = $script:ChecksumError } }
  return [pscustomobject]@{ Error = $null; Files = @(Get-Checksums (Get-DataPaths)); Db = $json }
}
function Compare-Snapshots($before, $after) {
  # Returns $null when config and data are the same, else the text that says where they differ.
  $problems = @()
  $b = @('#') + @($before.Files); $a = @('#') + @($after.Files)  # a sentinel: Compare-Object refuses an empty list
  if ($script:Data) {
    $root = $script:Data.TrimEnd('\') + '\'
    foreach ($m in $script:MayBeAdded) {
      $full = $root + $m
      $had = @($b | Where-Object { $_ -like "* $full" }).Count -gt 0
      if (-not $had) { $a = @($a | Where-Object { $_ -notlike "* $full" }) }  # added by the upgrade: allowed
    }
  }
  $d = @(Compare-Object -ReferenceObject $b -DifferenceObject $a)
  if ($d.Count -gt 0) {
    $paths = @($d | ForEach-Object { ($_.InputObject -replace '^\S+ ', '') } | Select-Object -Unique)
    $first = @($paths | Select-Object -First 5) -join ', '
    $more = if ($paths.Count -gt 5) { " and $($paths.Count - 5) more" } else { '' }
    $problems += "files changed: $first$more"
  }
  if ($before.Db -and -not $after.Db) { $problems += 'the database is gone' }
  elseif ($before.Db -and $after.Db) {
    $r = Invoke-LifecycleTool @('--db-compare', "`"$($before.Db)`"", "`"$($after.Db)`"")
    if ($r.Code -ne 0) { $problems += $r.Text }
    # Each change a migration declared on purpose (apps/controller/internal/store/declared_changes.go) that the comparison accepted.
    foreach ($l in @("$($r.Out)" -split "\r?\n" | Where-Object { $_ -like 'LC-11 note:*' })) { $script:Notes += $l.Trim() }
  }
  if ($problems.Count -eq 0) { return $null }
  return ($problems -join '; ')
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
  $v1 = Get-Version
  $before = Get-Snapshot 'before'
  if ($before.Error) { Out-Lc 'LC-10' $false $before.Error; return }
  [void](Start-Crsp)
  [void](Install-Msi $Pkg)
  $after = Get-Snapshot 'after'
  $v2 = Get-Version
  [void](Start-Crsp)
  if ($after.Error) { Out-Lc 'LC-10' $false $after.Error; return }
  $diff = Compare-Snapshots $before $after
  if (-not $diff -and $v1 -ne $v2) { $diff = "version changed from $v1 to $v2" }
  if ($diff) { Out-Lc 'LC-10' $false "reinstall of the same version changed config or data: $diff" }
  else { Out-Lc 'LC-10' $true 'reinstall of the same version changes nothing' }
}
function Lc11 {
  if (-not $PrevPkg) { Out-Lc 'LC-11' $false 'missing input PrevPkg'; return }
  [void](Remove-Msi -Purge); [void](Install-Msi $PrevPkg)
  $before = Get-Snapshot 'before'
  if ($before.Error) { Out-Lc 'LC-11' $false $before.Error; return }
  [void](Start-Crsp)
  $rc = Install-Msi $Pkg
  $after = Get-Snapshot 'after'
  [void](Start-Crsp)
  if ($after.Error) { Out-Lc 'LC-11' $false $after.Error; return }
  if ($rc -ne 0) { Out-Lc 'LC-11' $false "upgrade exit $rc"; return }
  if (-not $before.Files -and -not $before.Db) { Out-Lc 'LC-11' $false 'no config or data to compare after the previous version was installed'; return }
  $script:Notes = @()
  $diff = Compare-Snapshots $before $after
  foreach ($n in $script:Notes) { Write-Output $n }  # its own line in lifecycle-steps.txt
  if ($diff) { Out-Lc 'LC-11' $false "upgrade from N-1 lost or changed config or data: $diff" }
  else { Out-Lc 'LC-11' $true 'upgrade from N-1 keeps config and data (checksums)' }
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
  # LC-14 left nothing installed: install again, so there is a product to purge.
  [void](Install-Msi $Pkg)
  [void](Wait-Until { Test-Up } 60)
  if (-not (Get-ProductCode)) { Out-Lc 'LC-15' $false 'nothing installed to purge'; return }
  $rc = Remove-Msi -Purge
  $log = $script:LastMsiRemoveLog
  $out = & pwsh -NoProfile -File (Join-Path $script:Here 'leftovers.ps1') -Lifecycle $Lifecycle
  $ok = ($rc -eq 0) -and ($LASTEXITCODE -eq 0)
  if (-not $ok) { Show-MsiLog $log }
  Out-Lc 'LC-15' $ok $(if ($out) { 'leftovers: ' + ($out -join '; ') } else { 'purge leaves nothing' })
}
function Lc15Cli { # purge by the CLI: crsp uninstall --delete-data (OPS69)
  [void](Install-Msi $Pkg)
  $exe = Join-Path $script:Install 'crsp.exe'
  if (-not (Test-Path $exe)) { Out-Lc 'LC-15' $false 'crsp.exe is not installed for the --delete-data run'; return }
  & $exe uninstall --delete-data --yes | Out-Null
  $rc = $LASTEXITCODE
  # The CLI hands the removal to Windows Installer, detached (it ends this very exe); wait until the product is gone.
  $gone = Wait-Until { -not (Get-ProductCode) } 300
  # ...and until the msiexec it started has ended (the log it wrote says how it went).
  [void](Wait-Until { -not (Get-CimInstance Win32_Process -Filter "Name='msiexec.exe'" -ErrorAction SilentlyContinue | Where-Object { $_.CommandLine -match '/x\s*\{.*PURGE=1' }) } 120)
  [void](Remove-Msi -Purge)
  $out = & pwsh -NoProfile -File (Join-Path $script:Here 'leftovers.ps1') -Lifecycle $Lifecycle
  $cl = Join-Path $env:TEMP 'crsp-uninstall.log'
  if (Test-Path $cl) {
    Copy-Item -LiteralPath $cl -Destination (Get-LogDir) -Force -ErrorAction SilentlyContinue
    if (-not $gone -or $out) { Show-MsiLog $cl }
  }
  Out-Lc 'LC-15' ($gone -and ($rc -eq 0) -and ($LASTEXITCODE -eq 0)) $(if ($out) { 'cli leftovers: ' + ($out -join '; ') } else { 'crsp uninstall --delete-data leaves nothing' })
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
  # The pinned WiX 5.0.2 is a dotnet local tool (installer/controller/dotnet-tools.json), the way installer/controller/build.ps1
  # gets it; it is not on PATH. Call it the same way: restore, then `dotnet wix`. ICEs are never suppressed.
  $installerDir = Join-Path (Split-Path -Parent (Split-Path -Parent $script:Here)) 'installer/controller'
  $how = "dotnet tool restore, then dotnet wix msi validate, in $installerDir (manifest installer/controller/dotnet-tools.json)"
  $ice = $false
  $wixOk = $false
  if ((Test-Path (Join-Path $installerDir 'dotnet-tools.json')) -and (Get-Command dotnet -ErrorAction SilentlyContinue)) {
    $pkgFull = (Resolve-Path $Pkg).Path
    Show-MsiAllUsers $pkgFull
    Push-Location $installerDir
    try {
      & dotnet tool restore 2>&1 | Out-Host
      if ($LASTEXITCODE -eq 0) {
        $wixOk = $true
        & dotnet wix msi validate $pkgFull 2>&1 | Out-Host
        $ice = $LASTEXITCODE -eq 0
      }
    } catch {
      $how = "$how; $($_.Exception.Message)"
    } finally { Pop-Location }
  }
  if (-not $wixOk) { Out-Lc 'LC-18' $false "wix not found: $how"; return }
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
