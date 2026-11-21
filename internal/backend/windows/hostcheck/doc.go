// Package hostcheck holds the checks of the Windows backend that need a real
// Windows host (PowerShell compiling C#, the LSA policy). They run in the
// integration tier of scripts/ci/go-tiers.json, not in the 10 s unit tier of
// internal/backend/windows.
package hostcheck
