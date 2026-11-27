//go:build !windows

package windows

// nativeLookupSID is nil off Windows: the PowerShell script is used.
var nativeLookupSID func(string) (string, error)
