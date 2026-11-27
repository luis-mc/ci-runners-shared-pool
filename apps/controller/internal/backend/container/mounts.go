package container

import (
	"errors"
	"strings"
)

// CrspDirInContainer is the read-only folder that holds the crsp binary in a
// container job (runners R5.48, R5.84). A folder is mounted, never the file:
// a bind mount of one file keeps the old inode when the file is replaced and
// goes stale (handover lesson 14, D12).
const CrspDirInContainer = "/opt/crsp-bin"

// CrspBinEnv names the variable that tells the job hooks where crsp is.
const CrspBinEnv = "CRSP_BIN"

// CrspPathInContainer is where a container job finds the crsp binary for a
// host path: the folder mount point plus the file name.
func CrspPathInContainer(hostPath string) string {
	p := strings.ReplaceAll(hostPath, "\\", "/")
	return CrspDirInContainer + "/" + p[strings.LastIndex(p, "/")+1:]
}

// hostDir is the folder of a host file path, with the host's own separator.
func hostDir(hostPath string) string {
	i := strings.LastIndexAny(hostPath, "/\\")
	switch {
	case i < 0:
		return "."
	case i == 0:
		return hostPath[:1]
	case i == 2 && hostPath[1] == ':':
		return hostPath[:3]
	}
	return hostPath[:i]
}

// ErrSocketMount is returned for a mount of an engine socket (runners R6.1).
var ErrSocketMount = errors.New("an engine socket is never mounted into a job")

// CrspMountArgs returns the `docker run` arguments that mount the folder of the
// agent's own binary read-only at /opt/crsp-bin and set CRSP_BIN to the binary
// there. The binary is a linux binary of the agent's version. hostPath is the
// path of the binary the engine sees.
func CrspMountArgs(hostPath string) ([]string, error) {
	if hostPath == "" {
		return nil, errors.New("the crsp binary path is empty")
	}
	if IsSocketPath(hostPath) {
		return nil, ErrSocketMount
	}
	if strings.ContainsAny(hostPath, ":,\n") && !isWindowsDrivePath(hostPath) {
		return nil, errors.New("the crsp binary path has a character a mount cannot carry")
	}
	dir := hostDir(hostPath)
	if IsSocketPath(dir) {
		return nil, ErrSocketMount
	}
	return []string{"--mount", "type=bind,source=" + dir + ",target=" + CrspDirInContainer + ",readonly",
		"-e", CrspBinEnv + "=" + CrspPathInContainer(hostPath)}, nil
}

// IsSocketPath is true for a path of a Docker or Podman socket.
func IsSocketPath(p string) bool {
	p = strings.ToLower(strings.ReplaceAll(p, "\\", "/"))
	return strings.HasSuffix(p, "docker.sock") || strings.HasSuffix(p, "podman.sock") ||
		strings.Contains(p, "/var/run/docker") || strings.Contains(p, "docker_engine")
}

func isWindowsDrivePath(p string) bool {
	return len(p) > 2 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') && !strings.ContainsAny(p[2:], ":,\n")
}
