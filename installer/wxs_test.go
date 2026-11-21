// Package installer holds a check of the MSI authoring that runs on every OS.
package installer

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func wxs(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("crsp.wxs")
	if err != nil {
		t.Fatal(err)
	}
	// Comments may name what was removed; only the authored elements count.
	return regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(string(b), "")
}

// CRSP147: one entry in the Windows programs list, replaced by every upgrade. The package
// authors no second (hand-made) Uninstall key and hides nothing: its own entry is the only one.
func TestMSIHasOneProgramsListEntry(t *testing.T) {
	s := wxs(t)
	if strings.Contains(s, `CurrentVersion\Uninstall`) {
		t.Error("the package must not write its own Uninstall key: it would be a second entry next to the MSI's")
	}
	if strings.Contains(s, "ARPSYSTEMCOMPONENT") {
		t.Error("no hidden MSI entry: with one authored entry there is nothing to hide")
	}
}

// A newer MSI replaces the old product: one fixed UpgradeCode, a major upgrade that also
// replaces the same a.b.c, and a refused downgrade.
func TestMSIUpgradeReplacesTheOldProduct(t *testing.T) {
	s := wxs(t)
	if n := strings.Count(s, `UpgradeCode="C1D62442-6DB4-4FF0-9F28-A1CE3121B332"`); n != 1 {
		t.Errorf("the UpgradeCode must be the shipped one, once: %d", n)
	}
	if !strings.Contains(s, "<MajorUpgrade") || !strings.Contains(s, `AllowSameVersionUpgrades="yes"`) || !strings.Contains(s, "DowngradeErrorMessage") {
		t.Error("MajorUpgrade with same-version upgrades and a downgrade message is missing")
	}
	if strings.Contains(s, `Scope="perUser"`) || !strings.Contains(s, `Scope="perMachine"`) {
		t.Error("one per-machine package: a per-user twin would list twice")
	}
	if strings.Count(s, "<Package ") != 1 || strings.Contains(s, "<Bundle") {
		t.Error("one product: no bundle next to the MSI")
	}
}

// TRAY5 D12: the stock dialogs carry our art: a banner (493x58) and a dialog bitmap (493x312),
// both made by the build from the design tokens, never a file in the repository.
func TestMSIUsesTheGeneratedInstallerArt(t *testing.T) {
	s := wxs(t)
	for _, want := range []string{
		`<WixVariable Id="WixUIBannerBmp" Value="$(var.BannerBmp)" />`,
		`<WixVariable Id="WixUIDialogBmp" Value="$(var.DialogBmp)" />`,
		`<ui:WixUI Id="WixUI_Minimal" />`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s", want)
		}
	}
	b, err := os.ReadFile("build.ps1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"internal/updatewindow/genart", "packages/ui/src/tokens.css", "BannerBmp=", "DialogBmp="} {
		if !strings.Contains(string(b), want) {
			t.Errorf("build.ps1 does not wire %s", want)
		}
	}
}

var (
	reCustomAction = regexp.MustCompile(`<CustomAction\b[^>]*>`)
	reSetProperty  = regexp.MustCompile(`<SetProperty\b[^>]*>`)
	reCustom       = regexp.MustCompile(`<Custom\b[^>]*>`)
)

func attr(tag, name string) string {
	m := regexp.MustCompile(`\b` + name + `="([^"]*)"`).FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	return m[1]
}

// OPS50: no action may open a console window. Every command line that runs powershell.exe or
// cmd.exe goes through WixQuietExec (or WixQuietExec64); an action with ExeCommand never runs a shell.
func TestEveryShellActionRunsQuiet(t *testing.T) {
	s := wxs(t)
	quiet := map[string]bool{}
	for _, tag := range reCustomAction.FindAllString(s, -1) {
		id, dll, exe := attr(tag, "Id"), attr(tag, "DllEntry"), attr(tag, "ExeCommand")
		if dll == "WixQuietExec" || dll == "WixQuietExec64" {
			quiet[id] = true
		}
		if exe != "" || attr(tag, "FileRef") != "" {
			if strings.Contains(strings.ToLower(exe), "powershell") || strings.Contains(strings.ToLower(exe), "cmd") {
				t.Errorf("%s runs a shell through ExeCommand: a console window shows", id)
			}
		}
	}
	shells := 0
	for _, tag := range reSetProperty.FindAllString(s, -1) {
		id, val := attr(tag, "Id"), strings.ToLower(attr(tag, "Value"))
		if !strings.Contains(val, "powershell.exe") && !strings.Contains(val, `cmd.exe`) {
			continue
		}
		shells++
		if id != "WixQuietExecCmdLine" && id != "WixQuietExec64CmdLine" && !quiet[id] {
			t.Errorf("%s runs powershell.exe or cmd.exe but is not a WixQuietExec action", id)
		}
		if strings.HasPrefix(id, "WixQuietExec") && !quiet[attr(tag, "Before")] {
			t.Errorf("the immediate command line is set before %q, which is not a WixQuietExec action", attr(tag, "Before"))
		}
	}
	if shells == 0 {
		t.Fatal("no shell action found: the check reads nothing")
	}
	for _, tag := range reCustomAction.FindAllString(s, -1) {
		if attr(tag, "Property") != "" && attr(tag, "ExeCommand") != "" {
			for _, p := range reSetProperty.FindAllString(s, -1) {
				if attr(p, "Id") == attr(tag, "Property") && regexp.MustCompile(`(?i)powershell|cmd\.exe`).MatchString(attr(p, "Value")) {
					t.Errorf("%s starts a shell through a property", attr(tag, "Id"))
				}
			}
		}
	}
}

// OPS50: InstallValidate looks for files in use, so the step before it stops the agent service
// too (when it runs from the program folder), waits at most 20 s, and writes the same flag
// StopServiceEarly writes; StartServices2 and the rollback action read it to start the agent again.
func TestStepBeforeInstallValidateStopsTheAgentAndSetsTheFlag(t *testing.T) {
	s := wxs(t)
	var early string
	for _, tag := range reCustom.FindAllString(s, -1) {
		if attr(tag, "Before") == "InstallValidate" {
			early = attr(tag, "Action")
		}
	}
	if early == "" {
		t.Fatal("no action is sequenced Before=InstallValidate")
	}
	cmd := ""
	for _, tag := range reSetProperty.FindAllString(s, -1) {
		if attr(tag, "Id") == "WixQuietExecCmdLine" && attr(tag, "Before") == early {
			cmd = attr(tag, "Value")
		}
	}
	if cmd == "" {
		t.Fatalf("%s has no WixQuietExecCmdLine", early)
	}
	for _, want := range []string{
		"crsp-agent", "[INSTALLFOLDER]", "[DATAFOLDER]agent-was-running.flag", ".Stop()",
		"WaitForStatus('Stopped', '00:00:20')", "Name crsp ", "taskkill", "exit 0",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("%s: the command lacks %q", early, want)
		}
	}
	if strings.Index(cmd, "agent-was-running.flag") > strings.Index(cmd, "$g.Stop()") {
		t.Error("the flag must be written before the agent is stopped")
	}
	for _, id := range []string{"StartServices2", "StartServicesRollback"} {
		found := false
		for _, tag := range reSetProperty.FindAllString(s, -1) {
			if attr(tag, "Id") == id {
				found = true
				v := attr(tag, "Value")
				if !strings.Contains(v, "[DATAFOLDER]agent-was-running.flag") || !strings.Contains(v, "Start-Service -Name crsp-agent") {
					t.Errorf("%s does not start the agent from the flag", id)
				}
			}
		}
		if !found {
			t.Errorf("%s has no command", id)
		}
	}
}

// OPS59: an upgrade never removes the data folder (machines, keys, database): the package authors no
// RemoveFolder or RemoveFile, and the only deletion is the RemoveData action, which an upgrade never runs.
func TestMSINeverRemovesTheDataFolderOnUpgrade(t *testing.T) {
	s := wxs(t)
	for _, el := range []string{"<RemoveFolder", "<RemoveFile"} {
		if strings.Contains(s, el) {
			t.Errorf("%s would delete data the pool keeps between versions", el)
		}
	}
	if !strings.Contains(s, "NOT UPGRADINGPRODUCTCODE") {
		t.Error("RemoveData must not run on an upgrade: it needs NOT UPGRADINGPRODUCTCODE")
	}
}

// OPS69: remove asks through the native window, passes PURGE through, unregisters the runners and audits.
func TestMSIRemoveAsksPassesPurgeAndUnregistersRunners(t *testing.T) {
	s := wxs(t)
	for _, want := range []string{
		// the question is the native window, through the CLI; a cancel (exit 1) fails the action
		`uninstall --ask-to`,
		`Id="AskRemove"`,
		// interactive only: a silent uninstall keeps the data
		`UILevel&gt;=4`,
		// PURGE reaches the CLI as --delete-data
		`<Property Id="PURGE"`,
		`Value="--delete-data "`,
		`[PURGEARG]--actor`,
		// the runners are unregistered while the controller still runs
		`uninstall --runners-only`,
		`<Custom Action="UnregisterRunners" Before="CloseTray"`,
		// the older name keeps working
		`Condition="REMOVEDATA=&quot;1&quot;"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("crsp.wxs lacks %s", want)
		}
	}
	if strings.Contains(s, "RemoveDataDlg") || strings.Contains(s, `Type="CheckBox"`) {
		t.Error("the check box dialog is replaced by the native remove window")
	}
	// A cancel must stop the uninstall: the ask action returns "check", never "ignore".
	i := strings.Index(s, `Id="AskRemove"`)
	if i < 0 || !strings.Contains(s[i:i+400], `Return="check"`) {
		t.Error("AskRemove must be Return=check so Cancel ends the uninstall")
	}
	// The remove step never runs on an upgrade.
	i = strings.Index(s, `<Custom Action="RemoveData"`)
	if i < 0 || !strings.Contains(s[i:i+250], "NOT UPGRADINGPRODUCTCODE") {
		t.Error("RemoveData must not run on an upgrade")
	}
}
