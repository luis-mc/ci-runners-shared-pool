//go:build !windows

package updatewindow

// On macOS and Linux the update window is a web page in the browser (TRAY7): the same model
// and words as the Windows window. The remove question and the layout check draw with Windows
// fonts and stay Windows only.
func runWindow(c Config, o Options) int {
	if c.Mode == ModeRemove {
		say(o.Stderr, "The remove question is built for Windows.")
		return 1
	}
	return RunWeb(NewUpdateScreen(c, o.Service), o)
}

func layoutCheck(_ Config, o Options) int {
	say(o.Stderr, "The layout check draws with Windows fonts and runs on Windows only.")
	return 1
}
