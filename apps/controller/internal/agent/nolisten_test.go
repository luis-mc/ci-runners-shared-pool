package agent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// R5.9: the agent only dials out. No source file of the agent package opens a
// listening socket or an HTTP server.
func TestAgentOpensNoListeningPort(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	banned := []string{"net.Listen", "net.ListenTCP", "ListenAndServe", ".Serve(", "http.Server", "httptest.NewServer", "ListenPacket", "ListenUDP"}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, bad := range banned {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s contains %q: the agent must not listen", f, bad)
			}
		}
	}
}
