//go:build !windows

package load

// WinMemory implements Probe; it is only meaningful on Windows.
func (OSProbe) WinMemory() (float64, int64, error) { return 0, 0, ErrNoWindows }

// WinTimes implements Probe; it is only meaningful on Windows.
func (OSProbe) WinTimes() (Counters, error) { return Counters{}, ErrNoWindows }
