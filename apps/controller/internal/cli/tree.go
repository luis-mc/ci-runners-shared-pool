package cli

import (
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
)

// tree builds the command tree of the table in cli section 5.
func tree(env *Env) *command {
	root := &command{name: "crsp", summary: "CI Runners Shared Pool"}
	mode := func(name, summary, milestone string) *command {
		cmd := &command{name: name, summary: summary, raw: true, args: "[args]"}
		if fn := env.Modes[name]; fn != nil {
			cmd.run = func(c *call, args []string) error { return fn(c.env.Context, args) }
		} else {
			cmd.milestone = milestone
		}
		return cmd
	}
	root.subs = []*command{
		mode("controller", "run the controller: web console, API and scheduling", "M1"),
		mode("agent", "run the agent on a runner machine", "M1"),
		mode("tray", "run the tray in your session", "M8"),
		cmdVersion(env),
		cmdLogin(), cmdLogout(), cmdOpen(), cmdContext(),
		cmdApp(), cmdRepo(), cmdRepos(), cmdOrgPool(), cmdSettings(), cmdTrial(), cmdMachine(), cmdRunner(), cmdJob(),
		cmdDoctor(), cmdCompare(), cmdHistory(),
		cmdConfig(),
		cmdBackup(), cmdRestore(),
		cmdUpdate(), cmdRestart(),
		cmdUninstall("uninstall"), cmdUninstall("remove"),
		{name: "help", summary: "print usage", run: func(c *call, _ []string) error {
			writeUsage(c.env.Stdout, root, nil)
			return nil
		}},
	}
	root.subs = append(root.subs, cmdCompletion(root), cmdComplete())
	return root
}

func cmdVersion(env *Env) *command {
	return &command{name: "version", summary: "print the version",
		run: func(c *call, _ []string) error {
			if c.g.json {
				m := map[string]string{"version": env.Version, "go": env.GoVersion}
				if env.SchemaVersion > 0 {
					m["schema"] = strconv.FormatInt(env.SchemaVersion, 10)
				}
				return output.JSON(c.env.Stdout, m)
			}
			_, _ = fmt.Fprintf(c.env.Stdout, "crsp %s (%s)\n", env.Version, env.GoVersion)
			if env.SchemaVersion > 0 {
				_, _ = fmt.Fprintf(c.env.Stdout, "schema %d\n", env.SchemaVersion)
			}
			return nil
		}}
}

// walk lists every command path and its flags, for completion scripts.
func walk(cmd *command, prefix []string, out *[]string) {
	for _, s := range cmd.subs {
		if s.name == "__complete" {
			continue
		}
		*out = append(*out, strings.Join(append(append([]string{}, prefix...), s.name), " "))
		walk(s, append(append([]string{}, prefix...), s.name), out)
	}
}

func cmdCompletion(root *command) *command {
	return &command{name: "completion", summary: "print a shell completion script", args: "bash|zsh|fish|powershell",
		run: func(c *call, args []string) error {
			if len(args) != 1 {
				return fail(ExitUsage, "Usage: crsp completion bash|zsh|fish|powershell")
			}
			var paths []string
			walk(root, nil, &paths)
			top := []string{}
			for _, s := range root.subs {
				if s.name != "__complete" {
					top = append(top, s.name)
				}
			}
			var subs []string
			for _, s := range root.subs {
				if len(s.subs) > 0 {
					var n []string
					for _, x := range s.subs {
						n = append(n, x.name)
					}
					subs = append(subs, s.name+":"+strings.Join(n, " "))
				}
			}
			script, ok := completionScript(args[0], top, subs)
			if !ok {
				return fail(ExitUsage, "Unknown shell %q. Use bash, zsh, fish or powershell.", args[0])
			}
			_, _ = fmt.Fprint(c.env.Stdout, script)
			return nil
		}}
}

func completionScript(shell string, top, subs []string) (string, bool) {
	words := strings.Join(top, " ")
	flags := "--context --controller --json --no-color --timeout --yes --help"
	switch shell {
	case "bash":
		var cases strings.Builder
		for _, s := range subs {
			name, list, _ := strings.Cut(s, ":")
			fmt.Fprintf(&cases, "    %s) COMPREPLY=($(compgen -W \"%s %s\" -- \"$cur\")) ;;\n", name, list, flags)
		}
		return fmt.Sprintf(`# bash completion for crsp. Names come from the controller when it answers within 1 second.
_crsp() {
  local cur=${COMP_WORDS[COMP_CWORD]}
  if [ "$COMP_CWORD" -eq 1 ]; then COMPREPLY=($(compgen -W "%s" -- "$cur")); return; fi
  case "${COMP_WORDS[1]}" in
%s    *) COMPREPLY=($(compgen -W "%s" -- "$cur")) ;;
  esac
  if [ "$COMP_CWORD" -ge 3 ]; then
    COMPREPLY+=($(compgen -W "$(timeout 1 crsp __complete "${COMP_WORDS[1]}" 2>/dev/null)" -- "$cur"))
  fi
}
complete -F _crsp crsp
`, words, cases.String(), flags), true
	case "zsh":
		return fmt.Sprintf(`#compdef crsp
# zsh completion for crsp.
_crsp() {
  local -a cmds
  cmds=(%s)
  if (( CURRENT == 2 )); then _describe 'command' cmds; return; fi
  _values 'flag' %s
}
compdef _crsp crsp
`, words, flags), true
	case "fish":
		var b strings.Builder
		b.WriteString("# fish completion for crsp.\n")
		for _, t := range top {
			fmt.Fprintf(&b, "complete -c crsp -n '__fish_use_subcommand' -a %s\n", t)
		}
		for _, s := range subs {
			name, list, _ := strings.Cut(s, ":")
			fmt.Fprintf(&b, "complete -c crsp -n '__fish_seen_subcommand_from %s' -a '%s'\n", name, list)
		}
		return b.String(), true
	case "powershell":
		quoted := make([]string, len(top))
		for i, t := range top {
			quoted[i] = "'" + t + "'"
		}
		return fmt.Sprintf(`# PowerShell completion for crsp.
Register-ArgumentCompleter -Native -CommandName crsp -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  @(%s) | Where-Object { $_ -like "$wordToComplete*" } |
    ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) }
}
`, strings.Join(quoted, ",")), true
	}
	return "", false
}

// cmdComplete prints names for the completion scripts. It stays silent on any
// failure and gives up after one second, so a shell never waits on it (R5.25).
func cmdComplete() *command {
	return &command{name: "__complete", summary: "print names for shell completion", args: "repo|machine|runner",
		flags: func(c *call, fs *flag.FlagSet) {},
		run: func(c *call, args []string) error {
			c.g.timeout = 1_000_000_000
			if len(args) != 1 {
				return nil
			}
			s, err := c.open()
			if err != nil {
				return nil
			}
			switch args[0] {
			case "repo":
				repos, _ := listRepos(c.env.Context, s, clientListParams())
				for _, r := range repos {
					_, _ = fmt.Fprintln(c.env.Stdout, r.FullName)
				}
			case "machine":
				ms, _ := c.machines(s)
				for _, m := range ms {
					_, _ = fmt.Fprintln(c.env.Stdout, m.Name)
				}
			case "runner":
				rs, _ := c.runners(s, clientRunnersParams())
				for _, r := range rs {
					_, _ = fmt.Fprintln(c.env.Stdout, r.Name)
				}
			}
			return nil
		}}
}
