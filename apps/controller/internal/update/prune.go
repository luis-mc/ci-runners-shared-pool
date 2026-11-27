package update

import (
	"os"
	"path/filepath"
	"strconv"
)

// PruneVersions removes the downloaded version folders of the work folder (updates/<version>/)
// except those named in keep: after a good update only the current and the previous version
// stay (TRAY8). Other entries (the stage, result and plan files, logs, folders that are not a
// stable version) are never touched. It returns the versions it removed; a folder that cannot
// be removed stays and is tried again after the next update.
func PruneVersions(dir string, keep ...string) []string {
	keepSet := map[string]bool{}
	for _, k := range keep {
		if v, ok := ParseVersion(k); ok {
			keepSet[versionName(v)] = true
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var removed []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		v, ok := ParseVersion(e.Name())
		if !ok || e.Name() != versionName(v) || keepSet[e.Name()] {
			continue
		}
		if os.RemoveAll(filepath.Join(dir, e.Name())) == nil {
			removed = append(removed, e.Name())
		}
	}
	return removed
}

func versionName(v Version) string {
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
}
