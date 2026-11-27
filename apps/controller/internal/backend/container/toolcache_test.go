package container

import (
	"strings"
	"testing"
)

func TestToolCacheMountArgsMountReadWriteAndNameIt(t *testing.T) {
	a, err := ToolCacheMountArgs("/var/lib/crsp/tool-cache", true)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(a, " ")
	for _, want := range []string{"source=/var/lib/crsp/tool-cache,target=/crsp-tool-cache", "-e CRSP_TOOL_CACHE=/crsp-tool-cache", "-e CI_TOOLS_DIR=/crsp-tool-cache"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	if strings.Contains(text, "readonly") {
		t.Error("mounted read-only")
	}
	b, _ := ToolCacheMountArgs("C:\\ProgramData\\crsp\\tool-cache", false)
	if strings.Contains(strings.Join(b, " "), "CI_TOOLS_DIR") {
		t.Error("CI_TOOLS_DIR set although a lane gives it")
	}
	if len(b) == 0 {
		t.Error("a Windows drive path was refused")
	}
}

func TestToolCacheMountArgsRefuseBadPaths(t *testing.T) {
	if _, err := ToolCacheMountArgs("", true); err == nil {
		t.Error("empty accepted")
	}
	if _, err := ToolCacheMountArgs("/var/run/docker", true); err != ErrSocketMount {
		t.Errorf("socket: %v", err)
	}
	if _, err := ToolCacheMountArgs("/c,evil", true); err == nil {
		t.Error("comma accepted")
	}
}

func TestRunArgsMountsToolCacheAndStaysSafe(t *testing.T) {
	p := PoolSpec{Owner: "o", Repo: "r", ImageTag: "crsp/img:1", ToolCacheHost: "/var/lib/crsp/tool-cache"}
	args, err := RunArgs(p)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(args, " ")
	if !strings.Contains(text, "target=/crsp-tool-cache") || !strings.Contains(text, "CI_TOOLS_DIR=/crsp-tool-cache") {
		t.Errorf("no tool cache in %s", text)
	}
	p.ToolCacheHost = ""
	args, _ = RunArgs(p)
	if strings.Contains(strings.Join(args, " "), "crsp-tool-cache") {
		t.Error("mounted without a folder")
	}
}
