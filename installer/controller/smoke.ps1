<#
  Install / upgrade / downgrade / uninstall smoke test of the crsp MSI. Needs an
  elevated shell (it installs a per-machine package). Runs in CI on windows-latest
  (x64) and on a developer PC; it leaves the machine as it found it.

    pwsh installer/controller/smoke.ps1 -Msi v0.1.0/crsp-windows-x64.msi -UpgradeMsi v0.1.1/crsp-windows-x64.msi `
         -DowngradeMsi v0.0.9/crsp-windows-x64.msi -LogDir installer/controller/out/logs

#>
[CmdletBinding()]
param(
  [Parameter(Mandatory)][string]$Msi,
  [string]$UpgradeMsi = '',
  [string]$DowngradeMsi = '',
  [string]$FailingMsi = '',
  [string]$LogDir = (Join-Path $PSScriptRoot 'out\logs')
)
$ErrorActionPreference = 'Continue'
$Msi = (Resolve-Path $Msi).Path
if ($UpgradeMsi) { $UpgradeMsi = (Resolve-Path $UpgradeMsi).Path }
if ($DowngradeMsi) { $DowngradeMsi = (Resolve-Path $DowngradeMsi).Path }
if ($FailingMsi) { $FailingMsi = (Resolve-Path $FailingMsi).Path }
New-Item -ItemType Directory -Force -Path $LogDir | Out-Null
$LogDir = (Resolve-Path $LogDir).Path
$progDir = Join-Path $env:ProgramFiles 'crsp'
$dataDir = Join-Path $env:ProgramData 'crsp'
$ruleName = 'crsp console and agent link (private networks)'
$script:failures = 0

function Check([string]$what, [bool]$ok, [string]$detail = '') {
  if ($ok) { Write-Host "PASS  $what" } else { Write-Host "FAIL  $what  $detail"; $script:failures++ }
}
function Msiexec([string[]]$a, [string]$log) {
  $p = Start-Process msiexec.exe -ArgumentList ($a + @('/qn', '/norestart', '/l*v', "`"$log`"")) -Wait -PassThru
  return $p.ExitCode
}
function SvcState { (Get-Service crsp -ErrorAction SilentlyContinue).Status }
function WaitRunning([int]$sec = 40) {
  for ($i = 0; $i -lt $sec; $i++) { if ((SvcState) -eq 'Running') { Start-Sleep 2; if ((SvcState) -eq 'Running') { return $true } }; Start-Sleep 1 }
  return $false
}
# Every Apps-list entry of the product, from both registry views and from all users' uninstall keys.
# A hidden one (SystemComponent=1) is not listed by Windows, so it is not counted (CRSP147).
function ArpEntries {
  $roots = @('HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*', 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*')
  @(Get-ItemProperty $roots -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -eq 'CI Runners Shared Pool' -and $_.SystemComponent -ne 1 })
}
function Leftovers {
  $l = @()
  if (Test-Path $progDir) { $l += 'Program Files\crsp' }
  if (Get-Service crsp -ErrorAction SilentlyContinue) { $l += 'service crsp' }
  if (Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue) { $l += 'firewall rule' }
  $path = [Environment]::GetEnvironmentVariable('Path', 'Machine')
  if ($path -split ';' | Where-Object { $_.TrimEnd('\') -ieq $progDir }) { $l += 'PATH entry' }
  if (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run' -Name 'crsp-tray' -ErrorAction SilentlyContinue) { $l += 'tray autostart' }
  if (Test-Path (Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'CI Runners Shared Pool.lnk')) { $l += 'Start Menu shortcut' }
  if ((ArpEntries).Count -gt 0) { $l += 'ARP entry' }
  return $l
}

Write-Host "== clean start"
Check 'nothing installed before the run' ((Leftovers).Count -eq 0) ((Leftovers) -join ', ')

Write-Host "== install $Msi"
$rc = Msiexec @('/i', "`"$Msi`"") (Join-Path $LogDir 'install.log')
Check 'install exits 0' ($rc -eq 0) "exit $rc"
Check 'crsp.exe in Program Files' (Test-Path (Join-Path $progDir 'crsp.exe'))
$svc = Get-CimInstance Win32_Service -Filter "Name='crsp'"
Check 'service crsp registered' ($null -ne $svc)
if ($svc) {
  Check 'service is automatic' ($svc.StartMode -eq 'Auto') $svc.StartMode
  Check 'service runs crsp.exe controller' ($svc.PathName -match 'crsp\.exe"? controller$') $svc.PathName
  Check 'service account is LocalSystem' ($svc.StartName -eq 'LocalSystem') $svc.StartName
}
$qf = (sc.exe qfailure crsp) -join ' '
Check 'service restarts after a crash' ($qf -match 'RESTART') $qf
Check 'service is running' (WaitRunning) ("state " + (SvcState))
$path = [Environment]::GetEnvironmentVariable('Path', 'Machine')
Check 'install folder on the system PATH' ([bool]($path -split ';' | Where-Object { $_.TrimEnd('\') -ieq $progDir }))
$r = Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue
Check 'firewall rule exists' ($null -ne $r)
if ($r) {
  Check 'firewall rule is inbound allow, Private only' ($r.Direction -eq 'Inbound' -and $r.Action -eq 'Allow' -and [string]$r.Profile -eq 'Private') "dir $($r.Direction) action $($r.Action) profile $($r.Profile)"
  $pf = $r | Get-NetFirewallPortFilter
  Check 'firewall rule ports 8460,8461 tcp' ($pf.Protocol -eq 'TCP' -and (($pf.LocalPort | Sort-Object) -join ',') -eq '8460,8461') "$($pf.Protocol) $($pf.LocalPort -join ',')"
}
Check 'Start Menu shortcut' (Test-Path (Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'CI Runners Shared Pool.lnk'))
$run = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run' -Name 'crsp-tray' -ErrorAction SilentlyContinue).'crsp-tray'
Check 'tray autostart at sign-in' ($run -like '*crsp.exe" tray') $run
Check 'install-kind marker says msi' ((Get-Content (Join-Path $progDir 'install-kind') -Raw -ErrorAction SilentlyContinue).Trim() -eq 'msi')
Check 'data folder exists' (Test-Path $dataDir)
$acl = (Get-Acl $dataDir).Access | ForEach-Object { "$($_.IdentityReference)" }
Check 'data folder ACL has no Users group' (-not ($acl -match 'BUILTIN\\Users')) ($acl -join '; ')
Check 'data folder ACL is Administrators and SYSTEM only' (@($acl | Where-Object { $_ -notmatch 'BUILTIN\\Administrators|NT AUTHORITY\\SYSTEM' }).Count -eq 0) ($acl -join '; ')
$listen = $null
for ($i = 0; $i -lt 15 -and -not $listen; $i++) { $listen = Get-NetTCPConnection -LocalPort 8460 -State Listen -ErrorAction SilentlyContinue; if (-not $listen) { Start-Sleep 1 } }
Check 'controller listens on 8460' ($null -ne $listen)
Check 'the service wrote its log (logs\crsp.log)' (Test-Path (Join-Path $dataDir 'logs\crsp.log'))
$pid1 =(Get-CimInstance Win32_Service -Filter "Name='crsp'").ProcessId

if ($UpgradeMsi) {
  Set-Content -Path (Join-Path $dataDir 'keep-me.txt') -Value 'data' -Encoding ASCII
  Write-Host "== silent upgrade $UpgradeMsi"
  $rc = Msiexec @('/i', "`"$UpgradeMsi`"") (Join-Path $LogDir 'upgrade.log')
  Check 'upgrade exits 0' ($rc -eq 0) "exit $rc"
  Check 'service running after upgrade' (WaitRunning) ("state " + (SvcState))
  $pid2 = (Get-CimInstance Win32_Service -Filter "Name='crsp'").ProcessId
  Check 'service was restarted (new process)' ($pid2 -and $pid2 -ne $pid1) "pid $pid1 -> $pid2"
  Check 'data kept by upgrade' (Test-Path (Join-Path $dataDir 'keep-me.txt'))
  # The package's own entry is the only one; the upgrade replaced the old product's.
  $prod = ArpEntries
  Check 'one crsp entry in Apps' ($prod.Count -eq 1) "$($prod.Count)"
  Check 'firewall rule still there, once' (@(Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue).Count -eq 1)
  $path = [Environment]::GetEnvironmentVariable('Path', 'Machine')
  Check 'PATH has the folder once' (@($path -split ';' | Where-Object { $_.TrimEnd('\') -ieq $progDir }).Count -eq 1)
  $Msi = $UpgradeMsi
}
Check 'one crsp entry in Apps after install' ((ArpEntries).Count -eq 1) "$((ArpEntries).Count)"
$installedVersion = (ArpEntries | Select-Object -First 1).DisplayVersion
if ($DowngradeMsi) {
  Write-Host "== downgrade is refused $DowngradeMsi"
  $rc = Msiexec @('/i', "`"$DowngradeMsi`"") (Join-Path $LogDir 'downgrade.log')
  $dlog = Get-Content (Join-Path $LogDir 'downgrade.log') -Raw
  Check 'downgrade refused (exit 1603, "newer version" message)' ($rc -eq 1603 -and $dlog -match 'A newer version of CI Runners Shared Pool is already installed') "exit $rc"
  Check 'service still running after refused downgrade' ((SvcState) -eq 'Running')
}

if ($FailingMsi) {
  Write-Host "== a failed upgrade rolls back $FailingMsi"
  $rc = Msiexec @('/i', "`"$FailingMsi`"") (Join-Path $LogDir 'failed-upgrade.log')
  Check 'failed upgrade exits non-zero (1603)' ($rc -eq 1603) "exit $rc"
  Check 'old version still installed after rollback' (@(ArpEntries | Where-Object { $_.DisplayVersion -eq $installedVersion }).Count -eq 1 -and (ArpEntries).Count -eq 1) "wanted $installedVersion"
  Check 'service running again after rollback' (WaitRunning) ("state " + (SvcState))
  Check 'firewall rule and PATH intact after rollback' ($null -ne (Get-NetFirewallRule -DisplayName $ruleName -ErrorAction SilentlyContinue) -and [bool]([Environment]::GetEnvironmentVariable('Path','Machine') -split ';' | Where-Object { $_.TrimEnd('\') -ieq $progDir }))
  # What the log says about the rollback, so a failure can be read in the CI output.
  Select-String -Path (Join-Path $LogDir 'failed-upgrade.log') -Pattern 'Action start|Action ended|Rollback|return value 3|Error|error \d+|Removing|RemoveExistingProducts|Product:' |
    Where-Object { $_.Line -match 'RemoveExistingProducts|FailNow|Rollback|StartServicesRollback|InstallFiles|StartServices|return value 3|Error |Product:|InstallFinalize|InstallInitialize' } |
    Select-Object -First 30 | ForEach-Object { Write-Host "  LOG $($_.Line.Trim())" }
  Write-Host '--- end of the failed-upgrade log (last 70 lines)'
  Get-Content (Join-Path $LogDir 'failed-upgrade.log') -Tail 70 | ForEach-Object { Write-Host "  TAIL $_" }
  Write-Host '--- state after the failed upgrade'
  Write-Host ("  service: " + ((Get-Service crsp -ErrorAction SilentlyContinue | Out-String).Trim()))
  Write-Host ("  Program Files\crsp exists: " + (Test-Path $progDir))
  Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*' | Where-Object { $_.DisplayName -eq 'CI Runners Shared Pool' } | ForEach-Object { Write-Host "  ARP crsp $($_.DisplayVersion) SystemComponent=$($_.SystemComponent) $($_.PSChildName)" }
  Check 'msiexec log shows the rollback' ((Get-Content (Join-Path $LogDir 'failed-upgrade.log') -Raw) -match 'Rollback')
}

Write-Host "== silent uninstall keeps data"
if (-not (Test-Path (Join-Path $dataDir 'keep-me.txt'))) { Set-Content -Path (Join-Path $dataDir 'keep-me.txt') -Value 'data' -Encoding ASCII }
$rc = Msiexec @('/x', "`"$Msi`"") (Join-Path $LogDir 'uninstall.log')
Check 'uninstall exits 0' ($rc -eq 0) "exit $rc"
Check 'no leftovers after uninstall' ((Leftovers).Count -eq 0) ((Leftovers) -join ', ')
Check 'data kept by silent uninstall' (Test-Path (Join-Path $dataDir 'keep-me.txt'))

Write-Host "== reinstall over kept data, then uninstall with REMOVEDATA=1"
$rc = Msiexec @('/i', "`"$Msi`"") (Join-Path $LogDir 'reinstall.log')
Check 'reinstall exits 0' ($rc -eq 0) "exit $rc"
Check 'service running after reinstall' (WaitRunning)
Check 'data still there after reinstall' (Test-Path (Join-Path $dataDir 'keep-me.txt'))
$rc = Msiexec @('/x', "`"$Msi`"", 'REMOVEDATA=1') (Join-Path $LogDir 'uninstall-removedata.log')
Check 'uninstall with REMOVEDATA=1 exits 0' ($rc -eq 0) "exit $rc"
Check 'no leftovers' ((Leftovers).Count -eq 0) ((Leftovers) -join ', ')
Check 'data folder removed' (-not (Test-Path $dataDir)) 'still there'

# OPS69: PURGE=1 is the name the lifecycle standard uses; REMOVEDATA=1 above is the older alias.
Write-Host "== reinstall, then uninstall with PURGE=1"
$rc = Msiexec @('/i', "`"$Msi`"") (Join-Path $LogDir 'reinstall-purge.log')
Check 'reinstall exits 0' ($rc -eq 0) "exit $rc"
Check 'service running after reinstall' (WaitRunning)
New-Item -ItemType Directory -Force -Path $dataDir | Out-Null
Set-Content -Path (Join-Path $dataDir 'keep-me.txt') -Value 'x'
$rc = Msiexec @('/x', "`"$Msi`"", 'PURGE=1') (Join-Path $LogDir 'uninstall-purge.log')
Check 'uninstall with PURGE=1 exits 0' ($rc -eq 0) "exit $rc"
Check 'no leftovers after PURGE=1' ((Leftovers).Count -eq 0) ((Leftovers) -join ', ')
Check 'data folder removed by PURGE=1' (-not (Test-Path $dataDir)) 'still there'
Check 'remove audit row is in the Application event log' (@(Get-WinEvent -FilterHashtable @{ LogName = 'Application'; ProviderName = 'crsp' } -MaxEvents 20 -ErrorAction SilentlyContinue | Where-Object { $_.Message -match '"action":"remove"' }).Count -gt 0) 'no event'

if ($script:failures) { Write-Host "`n$($script:failures) check(s) FAILED"; exit 1 }
Write-Host "`nAll checks passed"
