//go:build !linux && !darwin && !windows

package power

import "context"

type osReader struct{}

// Read implements Reader: an OS without a reader counts as on mains.
func (osReader) Read(context.Context) State { return Mains }
