package memcap

import "runtime"

// ForHost gives the enforcer of the running system: the cgroup one under
// cgroupRoot on Linux, the job object one on Windows, the watcher on macOS (and
// any other system). The watcher needs a Tree and a Kill; the caller sets them.
func ForHost(cgroupRoot string, w *Watcher) Enforcer {
	return forOS(runtime.GOOS, cgroupRoot, w)
}

func forOS(goos, cgroupRoot string, w *Watcher) Enforcer {
	switch goos {
	case "linux":
		return CgroupEnforcer{Root: cgroupRoot}
	case "windows":
		return windowsEnforcer()
	}
	return w
}
