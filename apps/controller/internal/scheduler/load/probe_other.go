//go:build !linux && !darwin && !windows

package load

type osProbe struct{}

// NewProbe returns a probe that measures nothing: the gate then fails open (R5.22).
func NewProbe() Probe { return osProbe{} }

func (osProbe) Memory() (float64, bool)         { return 0, false }
func (osProbe) CPURaw() (Raw, bool)             { return Raw{}, false }
func (osProbe) LoadAvgPerCore() (float64, bool) { return 0, false }
