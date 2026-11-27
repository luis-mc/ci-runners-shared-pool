package hooks

import "os"

// NonInteractiveEnv is what every hook sets for itself and for anything it
// starts, so nothing a hook runs can stop to ask for a login (scheduling R5.62):
// git never prompts on the terminal and the Git Credential Manager never opens
// a dialog. A hook runs only local work; any remote step is optional, has a
// limit in seconds and fails open with a log line.
var NonInteractiveEnv = map[string]string{
	"GIT_TERMINAL_PROMPT": "0",
	"GCM_INTERACTIVE":     "never",
}

// NonInteractiveOK is the line a hook prints in probe mode after it has turned
// prompts off. The doctor reads it (doctor R5.30).
const NonInteractiveOK = "crsp hook non-interactive"

// applyNonInteractive turns prompts off for this process and its children.
func (e Env) applyNonInteractive() {
	set := e.Setenv
	if set == nil {
		set = func(k, v string) { _ = os.Setenv(k, v) }
	}
	for k, v := range NonInteractiveEnv {
		set(k, v)
	}
}
