//go:build linux

package power

import (
	"context"
	"os"
)

type osReader struct{}

// Read implements Reader over /sys/class/power_supply.
func (osReader) Read(context.Context) State {
	return ParseLinuxSupplies(os.DirFS("/sys/class/power_supply"))
}
