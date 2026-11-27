//go:build !linux

package tray

// HostCheck says whether a tray can be shown here.
type HostCheck int

// Results of ProbeHost.
const (
	HostOK HostCheck = iota
	HostNoSession
	HostNoTrayArea
)

// ProbeHost always reports a tray area off Linux: Windows and macOS always have one.
func ProbeHost() HostCheck { return HostOK }
