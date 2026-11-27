package windows

import (
	"regexp"
	"strings"
	"testing"
)

// allScripts is every PowerShell script the backend sends, by name.
func allScripts() map[string]string {
	return map[string]string{
		"account": scriptAccount, "accountSID": scriptAccountSID, "rights": scriptRights,
		"checkLogon": scriptCheckLogon, "rightsStatus": scriptRightsStatus, "extract": scriptExtract,
		"lock": scriptLock, "registerService": scriptRegisterService,
		"listeners": scriptListeners, "workers": scriptWorkers, "endListeners": scriptEndListeners,
		"startService": scriptStartService, "stopService": scriptStopService,
		"deleteService": scriptDeleteService, "removeAccount": scriptRemoveAccount, "find": scriptFind,
		"detect": scriptDetect, "tail": scriptTail, "usage": scriptUsage,
		"ensureLogon": scriptEnsureLogon, "logonRight": scriptLogonRight,
	}
}

var envUse = regexp.MustCompile(`\$env:(CRSP_[A-Z]+|ACTIONS_RUNNER_INPUT_TOKEN)`)

// OPS93: a script reads only the variables the backend sets.
func TestScriptsReadKnownVariables(t *testing.T) {
	known := map[string]bool{"CRSP_FOLDER": true, "CRSP_ACCOUNT": true, "CRSP_SERVICE": true, "CRSP_RUNNER": true,
		"CRSP_ARCHIVE": true, "CRSP_GROUP": true, "CRSP_PASSWORD": true, "CRSP_PIDS": true,
		"CRSP_SYMLINK": true, "CRSP_SID": true}
	for name, s := range allScripts() {
		if !strings.HasPrefix(s, scriptHeader) {
			t.Errorf("%s: no strict header", name)
		}
		for _, m := range envUse.FindAllStringSubmatch(s, -1) {
			if !known[m[1]] {
				t.Errorf("%s reads unknown variable %s", name, m[1])
			}
		}
	}
}
