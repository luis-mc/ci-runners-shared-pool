package api

import "testing"

// R5.52: settings, drain, resume, removal and the join-code routes need the admin
// or owner role; viewers see machines and load only. The contract says so per
// operation, and the auth middleware enforces x-auth.
func TestMachineRoutesDeclareTheRoleTheySpecRequires(t *testing.T) {
	want := map[string]string{
		"listMachines": "user", "getMachine": "user", "getMachineLoad": "user", "getMachineChange": "user",
		"updateMachine": "admin", "drainMachine": "admin", "resumeMachine": "admin", "restartMachineService": "admin", "removeMachine": "admin",
		"redetectMachine": "admin", "recoverMachine": "admin", "checkMachinePoolSize": "admin", "decideMachineChange": "admin",
		"runMachineCommand": "owner", "removeRunnerLeftovers": "admin", "createJoinCode": "admin", "joinMachineOverSsh": "admin", "getJoinCode": "admin", "revokeJoinCode": "admin", "getInstallCommand": "admin",
		"getCaCertificate": "public", "createJoinFile": "admin", "getPackage": "public", "listAgentInstallers": "user", "importAgentInstaller": "admin",
	}
	got := map[string]string{}
	for _, op := range loadContractOps(t) {
		if _, ok := want[op.ID]; ok {
			got[op.ID] = op.Auth
		}
	}
	for id, role := range want {
		if got[id] != role {
			t.Errorf("%s: x-auth = %q, want %q", id, got[id], role)
		}
	}
}
