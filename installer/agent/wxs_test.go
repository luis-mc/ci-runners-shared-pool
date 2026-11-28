// Package agentinstaller checks the agent MSI authoring on every OS (CRSP348).
package agentinstaller

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func wxs(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(string(b), "")
}

func TestAgentMSIIsItsOwnProduct(t *testing.T) {
	s := wxs(t, "windows/crspagent.wxs")
	c := wxs(t, "../controller/crsp.wxs")
	const controllerCode = "C1D62442-6DB4-4FF0-9F28-A1CE3121B332"
	m := regexp.MustCompile(`(?s)<Package [^>]*UpgradeCode="([0-9A-F-]{36})"`).FindStringSubmatch(s)
	if m == nil || m[1] == controllerCode || strings.Contains(c, m[1]) {
		t.Fatal("the agent MSI needs its own fixed UpgradeCode: sharing the Controller's would make one replace the other")
	}
	if !strings.Contains(s, "<MajorUpgrade") || !strings.Contains(s, "DowngradeErrorMessage") {
		t.Error("a newer agent MSI replaces the old one and a downgrade is refused")
	}
	if !strings.Contains(s, controllerCode) || !strings.Contains(s, "<Launch ") {
		t.Error("the agent MSI refuses to install next to the Controller")
	}
	for _, bad := range []string{"<ServiceInstall", "<Shortcut", "<ui:", "<UIRef", "fw:"} {
		if strings.Contains(s, bad) {
			t.Errorf("the agent MSI has no service, shortcut, dialog or firewall rule: found %s", bad)
		}
	}
	if !strings.Contains(s, `Name="crsp.exe"`) || !strings.Contains(s, `Name="crsp" `) {
		t.Error("crsp.exe goes to the program folder crsp, where the Controller puts it")
	}
}
