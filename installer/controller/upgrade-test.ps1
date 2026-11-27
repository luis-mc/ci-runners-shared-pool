<#
  Upgrade timing test of the crsp MSI (operations R5.86, task CRSP138). Needs an
  elevated shell on a Windows desktop session. It installs the previous MSI,
  starts the per-user tray from the installed crsp.exe, upgrades with
  `msiexec /i <new> /qn /norestart /l*v <log>`, and fails when

    - the upgrade takes longer than -MaxSeconds (default 60),
    - msiexec does not exit 0,
    - the log has a Files in Use entry (a file was held, so the run waited or asked for a reboot),
    - the old tray is still running afterwards, or the service is not running.

  It uninstalls at the end and leaves the machine as it found it.

    pwsh installer/controller/upgrade-test.ps1 -PreviousMsi v0.1.1/crsp-windows-amd64.msi -NewMsi new/crsp-windows-amd64.msi -LogDir installer/controller/out/logs
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory)][string]$PreviousMsi,
  [Parameter(Mandatory)][string]$NewMsi,
  [int]$MaxSeconds = 60,
  [string]$LogDir = (Join-Path $PSScriptRoot 'out\logs')
)
$ErrorActionPreference = 'Continue'
$PreviousMsi = (Resolve-Path $PreviousMsi).Path
$NewMsi = (Resolve-Path $NewMsi).Path
New-Item -ItemType Directory -Force -Path $LogDir | Out-Null
$LogDir = (Resolve-Path $LogDir).Path
$progDir = Join-Path $env:ProgramFiles 'crsp'
$exe = Join-Path $progDir 'crsp.exe'
$script:failures = 0

function Check([string]$what, [bool]$ok, [string]$detail = '') {
  if ($ok) { Write-Host "PASS  $what" } else { Write-Host "FAIL  $what  $detail"; $script:failures++ }
}
function Msiexec([string[]]$a, [string]$log) {
  $p = Start-Process msiexec.exe -ArgumentList ($a + @('/qn', '/norestart', '/l*v', "`"$log`"")) -Wait -PassThru
  return $p.ExitCode
}
function Trays {
  @(Get-CimInstance Win32_Process -Filter "Name='crsp.exe'" -ErrorAction SilentlyContinue |
    Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith($progDir, 'OrdinalIgnoreCase') -and $_.CommandLine -match ' tray' })
}

Write-Host "== install the previous version: $PreviousMsi"
$rc = Msiexec @('/i', "`"$PreviousMsi`"") (Join-Path $LogDir 'upgrade-previous-install.log')
Check 'previous version installs' ($rc -eq 0) "exit $rc"

Write-Host '== start the tray'
Start-Process $exe -ArgumentList 'tray' -WindowStyle Hidden | Out-Null
$old = @()
for ($i = 0; $i -lt 20 -and $old.Count -eq 0; $i++) { Start-Sleep -Milliseconds 500; $old = Trays }
Check 'the tray is running before the upgrade' ($old.Count -gt 0)
$oldIds = @($old | ForEach-Object { $_.ProcessId })

Write-Host '== upgrade'
$log = Join-Path $LogDir 'upgrade.log'
$sw = [Diagnostics.Stopwatch]::StartNew()
$rc = Msiexec @('/i', "`"$NewMsi`"") $log
$sw.Stop()
$secs = [math]::Round($sw.Elapsed.TotalSeconds, 1)
Write-Host "upgrade took $secs s"
Check 'upgrade exits 0' ($rc -eq 0) "exit $rc"
Check "upgrade takes at most $MaxSeconds s" ($secs -le $MaxSeconds) "took $secs s"
$text = if (Test-Path $log) { Get-Content $log -Raw } else { '' }
$fiu = [regex]::Matches($text, '(?im)^.*(Files in Use|MsiRMFilesInUse|Dialog created: FilesInUse).*$') | ForEach-Object { $_.Value.Trim() }
Check 'the log has no Files in Use entry' (@($fiu).Count -eq 0) ($fiu -join ' | ')
Check 'the old tray is gone' (@($oldIds | Where-Object { Get-Process -Id $_ -ErrorAction SilentlyContinue }).Count -eq 0)
Start-Sleep 3
Check 'the service runs after the upgrade' ((Get-Service crsp -ErrorAction SilentlyContinue).Status -eq 'Running')

# CRSP147: the upgrade replaces the old Apps-list entry; the old build's hand-made key is gone too.
$arp = @(Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*', 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*' -ErrorAction SilentlyContinue |
  Where-Object { $_.DisplayName -eq 'CI Runners Shared Pool' -and $_.SystemComponent -ne 1 })
Check 'one crsp entry in Apps after the upgrade' ($arp.Count -eq 1) ("$($arp.Count): " + (($arp | ForEach-Object { $_.DisplayVersion }) -join ', '))

Write-Host '== uninstall'
$rc = Msiexec @('/x', "`"$NewMsi`"") (Join-Path $LogDir 'upgrade-uninstall.log')
Check 'uninstall exits 0' ($rc -eq 0) "exit $rc"

if ($script:failures -gt 0) { Write-Host "$($script:failures) check(s) failed"; exit 1 }
Write-Host 'upgrade test passed'
