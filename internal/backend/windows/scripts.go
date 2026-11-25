package windows

// Every script is passed to powershell.exe on stdin (`-Command -`). Values
// come from environment variables (CRSP_*), never from a quoted command line
// (runners R5.18). The password is only ever CRSP_PASSWORD, set on the one
// process that needs it (runners R5.15).
const (
	scriptHeader = "$ErrorActionPreference = 'Stop'\n"

	// scriptAccount makes the group and the local account and sets its
	// password (runners R5.13). Its rights are set by the RightsGranter
	// (R5.14). The account is never an administrator. Needs CRSP_ACCOUNT,
	// CRSP_GROUP, CRSP_PASSWORD.
	scriptAccount = scriptHeader + `
if (-not (Get-LocalGroup -Name $env:CRSP_GROUP -ErrorAction SilentlyContinue)) {
    New-LocalGroup -Name $env:CRSP_GROUP -Description 'CRSP runner accounts' | Out-Null
}
if ($env:CRSP_ACCOUNT.Length -gt 20) { throw "the account name $($env:CRSP_ACCOUNT) is longer than the 20 characters Windows allows" }
$secure = ConvertTo-SecureString $env:CRSP_PASSWORD -AsPlainText -Force
if (Get-LocalUser -Name $env:CRSP_ACCOUNT -ErrorAction SilentlyContinue) {
    Set-LocalUser -Name $env:CRSP_ACCOUNT -Password $secure -PasswordNeverExpires $true -UserMayChangePassword $false -AccountNeverExpires
    Enable-LocalUser -Name $env:CRSP_ACCOUNT
} else {
    New-LocalUser -Name $env:CRSP_ACCOUNT -Password $secure -PasswordNeverExpires -UserMayNotChangePassword -AccountNeverExpires -Description 'CRSP runner account' | Out-Null
}
if (-not (Get-LocalGroupMember -Group $env:CRSP_GROUP -Member $env:CRSP_ACCOUNT -ErrorAction SilentlyContinue)) {
    Add-LocalGroupMember -Group $env:CRSP_GROUP -Member $env:CRSP_ACCOUNT
}
$made = Get-LocalUser -Name $env:CRSP_ACCOUNT -ErrorAction SilentlyContinue
if (-not $made) { throw "the account $($env:CRSP_ACCOUNT) was not found after it was made" }
if (-not $made.Enabled) { throw "the account $($env:CRSP_ACCOUNT) is disabled" }
Write-Output ('account-ready ' + $env:CRSP_ACCOUNT)
`

	// scriptAccountSID writes the SID of the account that was made, one line
	// with Write-Output (a [Console]::Out write is lost when the host's stdout
	// is redirected). The agent reads the SID in Go first (sid_windows.go); this
	// is the fallback. Needs CRSP_ACCOUNT.
	scriptAccountSID = scriptHeader + `
$user = Get-LocalUser -Name $env:CRSP_ACCOUNT -ErrorAction SilentlyContinue
if (-not $user) { throw "account not found: $($env:CRSP_ACCOUNT)" }
$sid = $user.SID.Value
if (-not $sid) { throw "no SID for the account $($env:CRSP_ACCOUNT)" }
Write-Output $sid
`

	// scriptRights sets the rights of the account through the LSA: log on as a
	// service, the two deny logons, and the symlink right to match the setting
	// (runners R5.14, R5.20). Only this one account is touched, and each right
	// is read back; a right that did not stick stops the add with its name.
	// Needs CRSP_ACCOUNT, CRSP_SYMLINK.
	scriptRights = scriptHeader + lsaSource + `
$want = @('SeServiceLogonRight', 'SeDenyInteractiveLogonRight', 'SeDenyRemoteInteractiveLogonRight')
[CrspLsa]::Add($env:CRSP_ACCOUNT, $want)
if ($env:CRSP_SYMLINK -eq '1') {
    [CrspLsa]::Add($env:CRSP_ACCOUNT, @('SeCreateSymbolicLinkPrivilege'))
} else {
    [CrspLsa]::Remove($env:CRSP_ACCOUNT, @('SeCreateSymbolicLinkPrivilege'))
}
foreach ($r in $want) {
    if (-not [CrspLsa]::Has($env:CRSP_ACCOUNT, $r)) { throw "the account $($env:CRSP_ACCOUNT) does not hold the right $r after it was set" }
}
`

	// scriptCheckLogon logs the account on as a service once, with the
	// password the service will get, before the service is made. A refusal
	// names its reason in plain words (no right to log on as a service, wrong
	// password, disabled account). Needs CRSP_ACCOUNT, CRSP_PASSWORD.
	scriptCheckLogon = scriptHeader + logonSource + `
$why = [CrspLogon]::Check($env:CRSP_ACCOUNT, $env:CRSP_PASSWORD)
if ($why) { throw "the account $($env:CRSP_ACCOUNT) cannot log on as a service: $why" }
`

	// scriptRightsStatus reads, without changing anything, whether the account
	// holds SeCreateSymbolicLinkPrivilege and whether Developer Mode is on
	// (runners R5.14). Prints `symlink=True;devmode=False`. Needs CRSP_ACCOUNT.
	scriptRightsStatus = scriptHeader + `
$sid = (New-Object System.Security.Principal.NTAccount($env:CRSP_ACCOUNT)).Translate([System.Security.Principal.SecurityIdentifier]).Value
$inf = Join-Path ([System.IO.Path]::GetTempPath()) ('crsp-rights-' + [guid]::NewGuid().ToString('N') + '.inf')
$db = Join-Path ([System.IO.Path]::GetTempPath()) ('crsp-rights-' + [guid]::NewGuid().ToString('N') + '.sdb')
try {
    secedit.exe /export /cfg $inf /areas USER_RIGHTS | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "secedit export failed with exit code $LASTEXITCODE" }
    $held = $false
    foreach ($l in (Get-Content -LiteralPath $inf)) {
        if ($l -match '^SeCreateSymbolicLinkPrivilege\s*=') {
            $held = (($l -split '=', 2)[1].Split(',') | ForEach-Object { $_.Trim() }) -contains ('*' + $sid)
        }
    }
    $dev = (Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\AppModelUnlock' -ErrorAction SilentlyContinue).AllowDevelopmentWithoutDevLicense -eq 1
    Write-Output ('symlink=' + $held + ';devmode=' + $dev)
} finally {
    Remove-Item -LiteralPath $inf, $db -Force -ErrorAction SilentlyContinue
}
`

	// scriptExtract unpacks the verified archive. CRSP_ARCHIVE, CRSP_FOLDER.
	scriptExtract = scriptHeader + `
New-Item -ItemType Directory -Force -Path $env:CRSP_FOLDER | Out-Null
Expand-Archive -LiteralPath $env:CRSP_ARCHIVE -DestinationPath $env:CRSP_FOLDER -Force
Remove-Item -LiteralPath $env:CRSP_ARCHIVE -Force
`

	// scriptLock grants Full control to the account, Administrators and SYSTEM
	// only, with inheritance off (runners R5.16). CRSP_FOLDER, CRSP_ACCOUNT.
	scriptLock = scriptHeader + `
$acl = New-Object System.Security.AccessControl.DirectorySecurity
$acl.SetAccessRuleProtection($true, $false)
$inherit = [System.Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit'
$none = [System.Security.AccessControl.PropagationFlags]::None
$allow = [System.Security.AccessControl.AccessControlType]::Allow
$full = [System.Security.AccessControl.FileSystemRights]::FullControl
foreach ($who in @($env:CRSP_ACCOUNT, 'BUILTIN\Administrators', 'NT AUTHORITY\SYSTEM')) {
    $rule = New-Object System.Security.AccessControl.FileSystemAccessRule($who, $full, $inherit, $none, $allow)
    $acl.AddAccessRule($rule)
}
Set-Acl -LiteralPath $env:CRSP_FOLDER -AclObject $acl
`

	// scriptRegisterService creates the service as the account and starts it
	// (runners R5.15, R5.18). CRSP_SERVICE, CRSP_FOLDER, CRSP_ACCOUNT,
	// CRSP_PASSWORD.
	scriptRegisterService = scriptHeader + `
$secure = ConvertTo-SecureString $env:CRSP_PASSWORD -AsPlainText -Force
$cred = New-Object System.Management.Automation.PSCredential(('.\' + $env:CRSP_ACCOUNT), $secure)
$exe = '"' + (Join-Path $env:CRSP_FOLDER 'bin\RunnerService.exe') + '"'
New-Service -Name $env:CRSP_SERVICE -BinaryPathName $exe -DisplayName ('CRSP runner ' + $env:CRSP_RUNNER) -Description 'GitHub Actions runner made by CI Runners Shared Pool' -StartupType Automatic -Credential $cred | Out-Null
try {
    Start-Service -Name $env:CRSP_SERVICE
} catch {
    $why = $_.Exception.Message
    $i = $_.Exception.InnerException
    while ($i) { $why += ' ' + $i.Message; $i = $i.InnerException }
    $svc = Get-CimInstance -ClassName Win32_Service -Filter ("Name='" + $env:CRSP_SERVICE.Replace("'", "''") + "'") -ErrorAction SilentlyContinue
    if ($svc) { $why += " (state $($svc.State), exit code $($svc.ExitCode), runs as $($svc.StartName))" }
    $ev = Get-WinEvent -FilterHashtable @{ LogName = 'System'; ProviderName = 'Service Control Manager'; StartTime = (Get-Date).AddMinutes(-2) } -ErrorAction SilentlyContinue |
        Where-Object { $_.Message -like ('*' + $env:CRSP_RUNNER + '*') -or $_.Message -like ('*' + $env:CRSP_SERVICE + '*') } | Select-Object -First 1
    if ($ev) { $why += ' Windows says: ' + $ev.Message }
    throw "the service did not start: $why"
}
`

	// scriptListeners prints the process id of every Runner.Listener that runs
	// from CRSP_FOLDER, one per line. The service wrapper is not counted: the
	// listener holds the runner's session with GitHub (runners R5.46).
	scriptListeners = scriptHeader + `
$want = (Join-Path (Join-Path $env:CRSP_FOLDER 'bin') 'Runner.Listener') + '.exe'
Get-Process -Name 'Runner.Listener' -ErrorAction SilentlyContinue |
    Where-Object { $_.Path -and ($_.Path -ieq $want) } |
    ForEach-Object { $_.Id }
`

	// scriptWorkers prints the process id of every Runner.Worker that runs from
	// CRSP_FOLDER: a job is running, so the listener must not be stopped.
	scriptWorkers = scriptHeader + `
$want = (Join-Path (Join-Path $env:CRSP_FOLDER 'bin') 'Runner.Worker') + '.exe'
Get-Process -Name 'Runner.Worker' -ErrorAction SilentlyContinue |
    Where-Object { $_.Path -and ($_.Path -ieq $want) } |
    ForEach-Object { $_.Id }
`

	// scriptEndListeners ends each process in CRSP_PIDS (comma separated) with
	// its whole process tree (runners R5.59).
	scriptEndListeners = scriptHeader + `
foreach ($id in ($env:CRSP_PIDS -split ',')) {
    if ($id) { & taskkill.exe /PID $id /T /F | Out-Null }
}
exit 0
`

	// scriptStartService starts the service. A failure names the Windows error
	// of every inner exception, so the logon failure (1069) can be told apart
	// and retried (runners R5.89). Needs CRSP_SERVICE.
	scriptStartService = scriptHeader + `
try {
    Start-Service -Name $env:CRSP_SERVICE
} catch {
    $m = $_.Exception.Message
    $i = $_.Exception.InnerException
    while ($i) {
        $m += ' ' + $i.Message
        if ($i -is [System.ComponentModel.Win32Exception]) { $m += ' (Windows error ' + $i.NativeErrorCode + ')' }
        $i = $i.InnerException
    }
    throw $m
}
`

	scriptStopService = scriptHeader + `
$svc = Get-Service -Name $env:CRSP_SERVICE -ErrorAction SilentlyContinue
if ($svc -and $svc.Status -ne 'Stopped') { Stop-Service -Name $env:CRSP_SERVICE -Force }
`

	// scriptDeleteService removes the service entry; safe when it is gone.
	scriptDeleteService = scriptHeader + `
$svc = Get-CimInstance -ClassName Win32_Service -Filter ("Name='" + $env:CRSP_SERVICE.Replace("'", "''") + "'") -ErrorAction SilentlyContinue
if ($svc) {
    if ($svc.State -ne 'Stopped') { Stop-Service -Name $env:CRSP_SERVICE -Force }
    $r = Invoke-CimMethod -InputObject $svc -MethodName Delete
    if ($r.ReturnValue -ne 0) { throw "deleting the service failed with code $($r.ReturnValue)" }
}
`

	// scriptRemoveAccount deletes the account; safe when it is gone.
	scriptRemoveAccount = scriptHeader + `
if (Get-LocalUser -Name $env:CRSP_ACCOUNT -ErrorAction SilentlyContinue) {
    Remove-LocalUser -Name $env:CRSP_ACCOUNT
    Write-Output 'account-removed'
} else {
    Write-Output 'account-absent'
}
`

	// scriptFind lists runner services as JSON (runners R5.5): the name, the
	// state, the path (which gives the folder) and StartName (the account).
	scriptFind = scriptHeader + `
$rows = @(Get-CimInstance -ClassName Win32_Service -Filter "Name LIKE 'actions.runner.%'" -ErrorAction SilentlyContinue |
    ForEach-Object { [pscustomobject]@{ Name = $_.Name; State = $_.State; PathName = $_.PathName; StartName = $_.StartName } })
ConvertTo-Json -InputObject $rows -Compress
`

	scriptDetect = scriptHeader + `
$id = [Security.Principal.WindowsIdentity]::GetCurrent()
$admin = ([Security.Principal.WindowsPrincipal]$id).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
Write-Output ('admin=' + $admin + ';ps=' + $PSVersionTable.PSVersion.ToString())
`

	// scriptTail reads the newest runner log lines for StreamLogs.
	scriptTail = scriptHeader + `
$log = Get-ChildItem -LiteralPath (Join-Path $env:CRSP_FOLDER '_diag') -Filter 'Runner_*.log' -ErrorAction SilentlyContinue | Sort-Object LastWriteTime | Select-Object -Last 1
if ($log) { Get-Content -LiteralPath $log.FullName -Tail 200 }
`

	// scriptUsage reads the service process' CPU and memory.
	scriptUsage = scriptHeader + `
$svc = Get-CimInstance -ClassName Win32_Service -Filter ("Name='" + $env:CRSP_SERVICE.Replace("'", "''") + "'") -ErrorAction SilentlyContinue
$cpu = 0; $mem = 0
if ($svc -and $svc.ProcessId) {
    $p = Get-Process -Id $svc.ProcessId -ErrorAction SilentlyContinue
    if ($p) { $cpu = [int64]$p.TotalProcessorTime.TotalMilliseconds; $mem = [int64]$p.PeakWorkingSet64 }
}
Write-Output ('cpu_ms=' + $cpu + ';mem=' + $mem)
`
)

// lsaSource is the C# of the one place that talks to the Local Security
// Authority: add, remove and read the rights of one account. It never touches
// another account and never the policy as a whole.
const lsaSource = `
Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Security.Principal;
public static class CrspLsa {
    [StructLayout(LayoutKind.Sequential)] struct ObjectAttributes { public int Length; public IntPtr Root; public IntPtr Name; public int Attributes; public IntPtr Descriptor; public IntPtr Quality; }
    [StructLayout(LayoutKind.Sequential)] struct UnicodeString { public ushort Length; public ushort MaximumLength; public IntPtr Buffer; }
    [DllImport("advapi32.dll")] static extern uint LsaOpenPolicy(IntPtr system, ref ObjectAttributes attrs, uint access, out IntPtr policy);
    [DllImport("advapi32.dll")] static extern uint LsaAddAccountRights(IntPtr policy, byte[] sid, UnicodeString[] rights, uint count);
    [DllImport("advapi32.dll")] static extern uint LsaRemoveAccountRights(IntPtr policy, byte[] sid, bool all, UnicodeString[] rights, uint count);
    [DllImport("advapi32.dll")] static extern uint LsaEnumerateAccountRights(IntPtr policy, byte[] sid, out IntPtr rights, out uint count);
    [DllImport("advapi32.dll")] static extern uint LsaFreeMemory(IntPtr buffer);
    [DllImport("advapi32.dll")] static extern uint LsaClose(IntPtr policy);
    [DllImport("advapi32.dll")] static extern uint LsaNtStatusToWinError(uint status);
    const uint NotFound = 0xC0000034;
    static void Check(uint status, string what) {
        if (status == 0) return;
        int err = (int)LsaNtStatusToWinError(status);
        throw new Exception(what + " failed: " + new Win32Exception(err).Message + " (Windows error " + err + ")");
    }
    static byte[] Sid(string account) {
        var sid = (SecurityIdentifier)new NTAccount(account).Translate(typeof(SecurityIdentifier));
        var b = new byte[sid.BinaryLength];
        sid.GetBinaryForm(b, 0);
        return b;
    }
    static IntPtr Open() {
        var a = new ObjectAttributes(); a.Length = Marshal.SizeOf(typeof(ObjectAttributes));
        IntPtr policy;
        Check(LsaOpenPolicy(IntPtr.Zero, ref a, 0x811, out policy), "opening the security policy");
        return policy;
    }
    static UnicodeString[] Strings(string[] rights) {
        var r = new UnicodeString[rights.Length];
        for (int i = 0; i < rights.Length; i++) {
            r[i].Buffer = Marshal.StringToHGlobalUni(rights[i]);
            r[i].Length = (ushort)(rights[i].Length * 2);
            r[i].MaximumLength = (ushort)(r[i].Length + 2);
        }
        return r;
    }
    static void Free(UnicodeString[] r) { foreach (var s in r) Marshal.FreeHGlobal(s.Buffer); }
    public static void Add(string account, string[] rights) {
        var sid = Sid(account); var p = Open(); var r = Strings(rights);
        try { Check(LsaAddAccountRights(p, sid, r, (uint)r.Length), "giving " + string.Join(", ", rights) + " to " + account); }
        finally { Free(r); LsaClose(p); }
    }
    public static void Remove(string account, string[] rights) {
        var sid = Sid(account); var p = Open(); var r = Strings(rights);
        try {
            uint st = LsaRemoveAccountRights(p, sid, false, r, (uint)r.Length);
            if (st != NotFound) Check(st, "taking " + string.Join(", ", rights) + " from " + account);
        } finally { Free(r); LsaClose(p); }
    }
    public static bool Has(string account, string right) {
        var sid = Sid(account); var p = Open();
        try {
            IntPtr buf; uint n;
            uint st = LsaEnumerateAccountRights(p, sid, out buf, out n);
            if (st == NotFound) return false;
            Check(st, "reading the rights of " + account);
            try {
                int size = Marshal.SizeOf(typeof(UnicodeString));
                for (int i = 0; i < n; i++) {
                    var u = (UnicodeString)Marshal.PtrToStructure(new IntPtr(buf.ToInt64() + (long)i * size), typeof(UnicodeString));
                    if (Marshal.PtrToStringUni(u.Buffer, u.Length / 2) == right) return true;
                }
                return false;
            } finally { LsaFreeMemory(buf); }
        } finally { LsaClose(p); }
    }
}
'@
`

// logonSource is the C# that logs an account on as a service once and names
// the reason in plain words when Windows refuses.
const logonSource = `
Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
public static class CrspLogon {
    [DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)] static extern bool LogonUser(string user, string domain, string password, int type, int provider, out IntPtr token);
    [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr h);
    public static string Check(string user, string password) {
        IntPtr token;
        if (LogonUser(user, ".", password, 5, 0, out token)) { CloseHandle(token); return ""; }
        int e = Marshal.GetLastWin32Error();
        string plain;
        switch (e) {
            case 1385: plain = "the account has no right to log on as a service"; break;
            case 1326: plain = "the password does not match the account"; break;
            case 1331: plain = "the account is disabled"; break;
            case 1330: plain = "the password of the account has expired"; break;
            case 1327: plain = "an account restriction blocks the logon"; break;
            default: plain = "Windows refused the logon"; break;
        }
        return plain + " (Windows error " + e + ": " + new Win32Exception(e).Message + ")";
    }
}
'@
`
