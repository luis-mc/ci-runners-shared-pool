package runnercache

import "path/filepath"

// EnvBrowsersPath is the folder Playwright keeps its browsers in (caches R5.41,
// runners R5.44).
const EnvBrowsersPath = "PLAYWRIGHT_BROWSERS_PATH"

// ImageBrowsersPath is where the `browsers` image layer holds the browsers. A
// job that finds them there downloads nothing.
const ImageBrowsersPath = "/opt/crsp/browsers"

// BrowsersDir is the one folder for Playwright browsers on a machine, inside
// the tool cache folder, so every runner and job of the machine shares it and
// the tool cache quota counts it (caches R5.41, R5.36). machineToolCache is the
// machine's tool cache folder.
func BrowsersDir(machineToolCache string) string {
	return filepath.Join(machineToolCache, "browsers")
}

// BrowsersEnv is the value the agent sets for every job. A runner whose image
// has the browsers layer keeps the image path; any other gets the machine's
// shared folder.
func BrowsersEnv(machineToolCache string, imageHasBrowsers bool) map[string]string {
	if imageHasBrowsers {
		return map[string]string{EnvBrowsersPath: ImageBrowsersPath}
	}
	return map[string]string{EnvBrowsersPath: BrowsersDir(machineToolCache)}
}
