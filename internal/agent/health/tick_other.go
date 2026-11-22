//go:build !windows

package health

import (
	"errors"
	"time"
)

func tickCount() (time.Duration, error) {
	return 0, errors.New("GetTickCount64 exists on Windows only")
}
