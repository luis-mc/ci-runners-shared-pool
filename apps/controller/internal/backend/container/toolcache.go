package container

import (
	"errors"
	"strings"
)

// The shared per-machine tool cache inside a pool container (caches R5.63).
const (
	// ToolCacheInside is where the machine's shared tool cache is mounted.
	ToolCacheInside = "/crsp-tool-cache"
	// EnvSharedToolCache names it for jobs.
	EnvSharedToolCache = "CRSP_TOOL_CACHE"
	envCIToolsDir      = "CI_TOOLS_DIR"
)

// ToolCacheMountArgs returns the `docker run` arguments that bind the machine's
// shared tool cache read-write and tell the job where it is. The folder is
// sticky and world-writable on the host (caches R5.18), so the container's
// uid can add an entry and cannot replace another's. When setToolsDir is true
// (no repository lane gives CI_TOOLS_DIR) the job's CI_TOOLS_DIR points at it
// too, as R5.14 says. An engine socket path is refused (runners R6.1).
func ToolCacheMountArgs(hostRoot string, setToolsDir bool) ([]string, error) {
	if hostRoot == "" {
		return nil, errors.New("the shared tool cache has no folder")
	}
	if IsSocketPath(hostRoot) {
		return nil, ErrSocketMount
	}
	if strings.ContainsAny(hostRoot, ",\n") || (strings.Contains(hostRoot, ":") && !isWindowsDrivePath(hostRoot)) {
		return nil, errors.New("the shared tool cache path has a character a mount cannot carry")
	}
	a := []string{"--mount", "type=bind,source=" + hostRoot + ",target=" + ToolCacheInside,
		"-e", EnvSharedToolCache + "=" + ToolCacheInside}
	if setToolsDir {
		a = append(a, "-e", envCIToolsDir+"="+ToolCacheInside)
	}
	return a, nil
}
