package install

import "runtime"

var goos = runtime.GOOS

var debArch = func() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "amd64"
}()
