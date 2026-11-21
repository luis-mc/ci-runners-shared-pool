package windows

// HostCheckScript is the PowerShell script the host check runs on a real
// Windows machine (package hostcheck, integration tier): the C# of the LSA and
// logon scripts must compile and read the rights of a built-in account.
// Nothing is changed. It is here, not in the test, because it uses the
// unexported script parts; no production code calls it.
func HostCheckScript() string {
	return wrapScript(scriptHeader + lsaSource + logonSource + `
if (-not [CrspLsa]::Has('BUILTIN\Administrators', 'SeBackupPrivilege')) { throw 'Administrators should hold SeBackupPrivilege' }
if ([CrspLsa]::Has('BUILTIN\Administrators', 'SeDenyInteractiveLogonRight')) { throw 'Administrators must not be denied' }
if (-not [CrspLogon]::Check('crsp-no-such-account', 'x')) { throw 'a missing account must be refused' }
[Console]::Out.WriteLine('ok')
`)
}
