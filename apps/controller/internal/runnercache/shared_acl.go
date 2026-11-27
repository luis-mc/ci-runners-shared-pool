package runnercache

import "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"

// RunnersGroup is the local group every runner account belongs to (runners R5.14).
const RunnersGroup = "crsp-runners"

// SharedACLPlan is the `icacls` call that makes the shared root on Windows
// (caches R5.19): the runner accounts may create files and folders (write data
// and append data, inherited), read what is there, and may not change or
// delete: only the creator may (CREATOR OWNER gets full control of what it
// creates). Inheritance from the parent is cut so no wider right leaks in.
func SharedACLPlan(root string) sys.Command {
	return sys.Command{Name: "icacls", Args: []string{
		root, "/inheritance:r",
		"/grant", "SYSTEM:(OI)(CI)(F)",
		"/grant", "Administrators:(OI)(CI)(F)",
		"/grant", "CREATOR OWNER:(OI)(CI)(IO)(F)",
		"/grant", RunnersGroup + ":(OI)(CI)(RX,WD,AD)",
	}}
}
