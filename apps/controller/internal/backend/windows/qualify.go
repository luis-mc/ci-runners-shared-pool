package windows

import "strings"

// QualifyAccount returns MACHINE\name for a bare local account name, so a
// lookup never reaches for a domain; an already qualified name (or a `.\`
// prefix) is rewritten to the machine. An empty machine returns the bare name.
func QualifyAccount(machine, account string) string {
	name := strings.TrimSpace(account)
	if i := strings.LastIndex(name, `\`); i >= 0 {
		name = name[i+1:]
	}
	if machine == "" {
		return name
	}
	return machine + `\` + name
}
