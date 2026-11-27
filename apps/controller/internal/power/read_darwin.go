//go:build darwin

package power

import (
	"context"
	"os/exec"
	"time"
)

type osReader struct{}

// Read implements Reader with `pmset -g batt`.
func (osReader) Read(ctx context.Context) State {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/pmset", "-g", "batt").Output()
	if err != nil {
		return Mains
	}
	return ParsePmset(string(out))
}
