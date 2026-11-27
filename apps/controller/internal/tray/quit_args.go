package tray

import (
	"path/filepath"
	"strings"
)

// isTrayCmdline says whether a command line (argv) is `crsp tray ...` of the
// executable in dir. The compare is case-insensitive on the folder, as on Windows.
func isTrayCmdline(argv []string, dir string) bool {
	if len(argv) < 2 || argv[1] != "tray" {
		return false
	}
	for _, a := range argv[2:] {
		if a == "--quit" || a == "-quit" {
			return false // the quitter itself
		}
	}
	return strings.EqualFold(filepath.Dir(argv[0]), dir)
}
